package protocol

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"
)

const MaxCommandBytes = 1 << 20

// Allow Chrome's 64MiB image reply plus echoed request metadata and JSON framing.
// JSON escaping can expand a bounded request's id/epoch by at most six times.
const MaxResponseBytes = (64 << 20) + 6*MaxCommandBytes + 1024
const DaemonName = "daemon.tether.internal"

var tlsMaterial struct {
	sync.Mutex
	token          string
	client, server *tls.Config
}

// DaemonTLS derives a private trust root and role-specific identities from the
// SSH-delivered key. Only standard TLS chain, name and usage verification is used.
func DaemonTLS(token string, server bool) (*tls.Config, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("daemon authentication key is missing")
	}
	tlsMaterial.Lock()
	defer tlsMaterial.Unlock()
	if tlsMaterial.token != token {
		tlsMaterial.token = token
		tlsMaterial.client, tlsMaterial.server = nil, nil
	}
	if server && tlsMaterial.server != nil {
		return tlsMaterial.server.Clone(), nil
	}
	if !server && tlsMaterial.client != nil {
		return tlsMaterial.client.Clone(), nil
	}
	key := func(role string) ed25519.PrivateKey {
		seed := sha256.Sum256([]byte("tether-browser/daemon-tls/v1/" + role + "\x00" + token))
		return ed25519.NewKeyFromSeed(seed[:])
	}
	rootKey := key("root")
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Tether private root"}, NotBefore: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2120, 1, 1, 0, 0, 0, 0, time.UTC), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, root, root, rootKey.Public(), rootKey)
	if err != nil {
		return nil, err
	}
	root, err = x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	role, serial, usage := "client", int64(3), x509.ExtKeyUsageClientAuth
	if server {
		role, serial, usage = "server", 2, x509.ExtKeyUsageServerAuth
	}
	leafKey := key(role)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: role}, DNSNames: []string{DaemonName}, NotBefore: root.NotBefore, NotAfter: root.NotAfter, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, leafKey.Public(), rootKey)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, der}, PrivateKey: leafKey}}, RootCAs: roots, ServerName: DaemonName}
	if server {
		cfg.ClientCAs = roots
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	if server {
		tlsMaterial.server = cfg
	} else {
		tlsMaterial.client = cfg
	}
	return cfg, nil
}

// AuthenticateDaemon finishes authentication before any command is sent.
func AuthenticateDaemon(ctx context.Context, conn net.Conn, token string) (*tls.Conn, error) {
	cfg, err := DaemonTLS(token, false)
	if err != nil {
		return nil, err
	}
	secured := tls.Client(conn, cfg)
	handshake, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := secured.HandshakeContext(handshake); err != nil {
		return nil, fmt.Errorf("authenticate daemon: %w", err)
	}
	return secured, nil
}

// ReadFrame bounds newline-delimited JSON before parsing; it never drains an
// untrusted oversized frame. The caller closes that connection on error.
func ReadFrame(reader *bufio.Reader, limit int) ([]byte, error) {
	var frame []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(frame)+len(part) > limit {
			return nil, errors.New("RPC frame exceeds size limit")
		}
		frame = append(frame, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, err
		}
		return frame, nil
	}
}

func ValidateResponse(req *Request, resp *Response) error {
	if resp.JSONRPC != JSONRPCVersion || !bytes.Equal(bytes.TrimSpace(req.ID), bytes.TrimSpace(resp.ID)) || req.Seq != resp.Seq || req.Epoch != resp.Epoch {
		return errors.New("daemon response does not match request id, sequence or epoch")
	}
	return nil
}

func ReadResponse(conn net.Conn, req *Request) (*Response, error) {
	data, err := ReadFrame(bufio.NewReader(conn), MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	var resp Response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if err := ValidateResponse(req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
