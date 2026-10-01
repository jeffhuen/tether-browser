package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/jeffhuen/tether-browser/packages/cli"
	"github.com/jeffhuen/tether-browser/packages/protocol"
)

// OpenSSH rejects Unix -D in its multiplex forwarding API. Keep the original
// master's authentication configuration and use documented -W stdio channels.
// ponytail: one mux client per TCP connection; benchmark before replacing it.
func openPrivateSSHProxy(ctx context.Context, control, host, socketPath string) (func(), error) {
	if err := protocol.ValidatePrivateSocket(control); err != nil {
		return nil, err
	}
	if err := protocol.ValidatePrivateDir(filepath.Dir(socketPath)); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	proxyCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(proxyCtx, func() { _ = ln.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer stop()
		var workers sync.WaitGroup
		for {
			conn, err := ln.Accept()
			if err != nil {
				break
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				servePrivateSSHConn(proxyCtx, conn.(*net.UnixConn), control, host)
			}()
		}
		workers.Wait()
	}()
	return func() { cancel(); _ = ln.Close(); <-done }, nil
}

func servePrivateSSHConn(ctx context.Context, conn *net.UnixConn, control, host string) {
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if protocol.VerifyPeerCredentials(conn) != nil {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	address, err := readPrivateSSHAddress(conn)
	if err != nil {
		return
	}
	if _, err := sshSOCKSAddress(address); err != nil {
		return
	}
	if err := protocol.ValidatePrivateSocket(control); err != nil {
		return
	}
	file, err := conn.File()
	if err != nil {
		return
	}
	defer file.Close()
	// Write the SOCKS reply before giving SSH the output descriptor: a remote
	// service may send data immediately (for example an SSH greeting).
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	cmd := exec.CommandContext(ctx, "ssh", "-F", "none", "-S", control,
		"-o", "ControlMaster=no", "-o", "ControlPersist=no", "-o", "ProxyCommand=false",
		"-o", "ProxyJump=none", "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes",
		"-o", "ExitOnForwardFailure=yes", "-W", address, "--", host)
	cli.ManageCommand(cmd)
	cmd.Stdin, cmd.Stdout = file, file
	cmd.Stderr = &boundedOutput{limit: 4096}
	if err := cmd.Start(); err != nil {
		return
	}
	defer cli.KillCommand(cmd)
	// The mux client owns the data descriptors; no payload copying in this helper.
	_ = cmd.Wait()
}

func readPrivateSSHAddress(conn net.Conn) (string, error) {
	var greeting [3]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil {
		return "", err
	}
	if greeting != [3]byte{5, 1, 0} {
		return "", errors.New("invalid private SOCKS greeting")
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return "", err
	}
	var head [4]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return "", err
	}
	if head[0] != 5 || head[1] != 1 || head[2] != 0 {
		return "", errors.New("invalid private SOCKS request")
	}
	size := 0
	switch head[3] {
	case 1:
		size = 4
	case 4:
		size = 16
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return "", err
		}
		size = int(length[0])
		if size == 0 {
			return "", errors.New("empty private SOCKS hostname")
		}
	default:
		return "", errors.New("invalid private SOCKS address")
	}
	var address [257]byte
	if _, err := io.ReadFull(conn, address[:size+2]); err != nil {
		return "", err
	}
	host := string(address[:size])
	if head[3] != 3 {
		host = net.IP(address[:size]).String()
	}
	port := binary.BigEndian.Uint16(address[size : size+2])
	if port == 0 {
		return "", errors.New("invalid private SOCKS port")
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}
