//go:build linux || darwin

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jeffhuen/tether-browser/packages/protocol"
)

func TestDisconnectedScreenshotStaysLocalAndReportsRemoteFailure(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	bytes := []byte{0, 1, 2, 3}
	request, _ := json.Marshal(map[string]string{"filename": "shot.png", "base64": base64.StdEncoding.EncodeToString(bytes)})
	result, err := handleScreenshotSystem(context.Background(), &sshSession{}, "system_save_screenshot", request)
	if err != nil {
		t.Fatal(err)
	}
	saved := result.(map[string]any)
	if saved["mirrored"] != false || saved["remotePath"] != nil || saved["mirrorError"] == nil {
		t.Fatalf("false remote success: %+v", saved)
	}
	path := saved["localPath"].(string)
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(bytes) {
		t.Fatalf("local capture lost: %v", err)
	}
	if err := protocol.ValidatePrivateFile(path); err != nil {
		t.Fatal(err)
	}
	deleted, err := handleScreenshotSystem(context.Background(), &sshSession{}, "system_delete_screenshot", []byte(`{"filename":"shot.png"}`))
	if err != nil {
		t.Fatal(err)
	}
	status := deleted.(map[string]any)
	if status["ok"] != false || status["localDeleted"] != true || status["remoteDeleted"] != false || status["remoteError"] == nil {
		t.Fatalf("delete lied about remote: %+v", status)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("local deletion did not happen")
	}
}

func TestScreenshotNamesAndSymlinksFailClosed(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	for _, name := range []string{"../shot.png", "x/y.png", `x\y.png`, "..", "a..png", "x\x00.png", "x\n.png"} {
		request, _ := json.Marshal(map[string]string{"filename": name, "base64": "AA=="})
		if _, err := handleScreenshotSystem(context.Background(), &sshSession{}, "system_save_screenshot", request); err == nil {
			t.Fatalf("unsafe name accepted: %q", name)
		}
	}
	dir := localScreenshotsDir()
	if err := protocol.PrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(cache, "victim")
	if err := os.WriteFile(victim, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "shot.png")); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"system_save_screenshot", "system_delete_screenshot", "system_clear_screenshots"} {
		if _, err := handleScreenshotSystem(context.Background(), &sshSession{}, kind, []byte(`{"filename":"shot.png","base64":"AA=="}`)); err == nil {
			t.Fatalf("%s accepted symlink", kind)
		}
	}
	data, err := os.ReadFile(victim)
	if err != nil || string(data) != "original" {
		t.Fatal("symlink victim changed")
	}
}

func TestRemoteScreenshotCacheIsPrivateAndReportsResolvedPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("SHELL", "/bin/sh")
	cache := filepath.Join(root, "xdg")
	if err := os.Mkdir(cache, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cache, filepath.Join(root, "cache-link")); err != nil {
		t.Fatal(err)
	}
	profile := `export XDG_CACHE_HOME="$HOME/cache-link"
printf 'noisy profile must not become remotePath\n'
`
	if err := os.WriteFile(filepath.Join(root, ".profile"), []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	save := func() ([]byte, error) {
		cmd := exec.Command("sh", "-c", remoteScreenshotSaveCommand("shot.png"))
		cmd.Stdin = strings.NewReader("image")
		return cmd.Output()
	}
	out, err := save()
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(root, "xdg", "tether", "screenshots", "shot.png")
	if strings.TrimSpace(string(out)) != expected {
		t.Fatalf("wrong resolved remote path: %q", out)
	}
	if err := protocol.ValidatePrivateFile(expected); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(expected); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, expected); err != nil {
		t.Fatal(err)
	}
	if _, err := save(); err == nil {
		t.Fatal("remote mirror followed symlink")
	}
	deleteShot := exec.Command("sh", "-c", remoteScreenshotDeleteCommand("shot.png", expected))
	if deleteShot.Run() == nil {
		t.Fatal("remote deletion hid unsafe file failure")
	}
	data, err := os.ReadFile(victim)
	if err != nil || string(data) != "original" {
		t.Fatal("remote symlink victim changed")
	}
}

func legacyScreenshotCache(t *testing.T) (string, string) {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	dir := filepath.Join(cache, "tether", "screenshots")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Dir(dir), dir} {
		if err := os.Chmod(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	shot := filepath.Join(dir, "legacy.png")
	if err := os.WriteFile(shot, []byte("legacy image"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shot, 0644); err != nil {
		t.Fatal(err)
	}
	return dir, shot
}

func TestLegacyGalleryOperationsMigrateWithoutManualPermissions(t *testing.T) {
	for _, kind := range []string{"system_save_screenshot", "system_delete_screenshot", "system_clear_screenshots"} {
		t.Run(kind, func(t *testing.T) {
			dir, shot := legacyScreenshotCache(t)
			for path, mode := range map[string]os.FileMode{filepath.Dir(dir): 0750, dir: 0750, shot: 0640} {
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
			}
			result, err := handleScreenshotSystem(context.Background(), &sshSession{}, kind, []byte(`{"filename":"legacy.png","base64":"bmV3IGltYWdl"}`))
			if err != nil {
				t.Fatal(err)
			}
			if err := protocol.ValidatePrivateDir(filepath.Dir(dir)); err != nil {
				t.Fatal(err)
			}
			if err := protocol.ValidatePrivateDir(dir); err != nil {
				t.Fatal(err)
			}
			if kind == "system_save_screenshot" {
				if err := protocol.ValidatePrivateFile(shot); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(shot)
				if err != nil || string(data) != "new image" {
					t.Fatalf("legacy capture was not replaced: %q %v", data, err)
				}
			} else {
				if _, err := os.Lstat(shot); !os.IsNotExist(err) {
					t.Fatalf("legacy local capture remains: %v", err)
				}
				status := result.(map[string]any)
				if status["localDeleted"] != true || status["remoteDeleted"] != false {
					t.Fatalf("destination-free cleanup claimed remote success: %+v", status)
				}
			}
		})
	}
}

func TestLegacyGalleryRefusesWritableAndForeignEntries(t *testing.T) {
	for _, unsafe := range []string{"writable", "foreign"} {
		t.Run(unsafe, func(t *testing.T) {
			_, shot := legacyScreenshotCache(t)
			if unsafe == "writable" {
				if err := os.Chmod(shot, 0666); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Chown(shot, os.Getuid()+1, -1); err != nil {
				t.Skipf("foreign-owner fixture requires chown permission: %v", err)
			}
			for _, kind := range []string{"system_save_screenshot", "system_delete_screenshot", "system_clear_screenshots"} {
				if _, err := handleScreenshotSystem(context.Background(), &sshSession{}, kind, []byte(`{"filename":"legacy.png","base64":"AA=="}`)); err == nil {
					t.Fatalf("%s accepted unsafe legacy entry", kind)
				}
			}
			data, err := os.ReadFile(shot)
			if err != nil || string(data) != "legacy image" {
				t.Fatalf("unsafe entry changed: %q %v", data, err)
			}
		})
	}
}

func remoteScreenshotFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	cache := filepath.Join(root, "remote-cache")
	legacy := filepath.Join(root, "tether-screenshots")
	t.Setenv("HOME", root)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "local-cache"))
	profile := "export XDG_CACHE_HOME=\"$HOME/remote-cache\"\n"
	if err := os.WriteFile(filepath.Join(root, ".profile"), []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(cache, "tether", "screenshots"), legacy
}

func TestRemoteLegacyDeletionPreservesNewCacheCollisionAndUnlistedCopies(t *testing.T) {
	_, dir, legacy := remoteScreenshotFixture(t)
	if err := os.Chmod(legacy, 0750); err != nil {
		t.Fatal(err)
	}
	save := exec.Command("sh", "-c", remoteScreenshotSaveCommand("shot.png"))
	save.Stdin = strings.NewReader("new image")
	if out, err := save.Output(); err != nil || strings.TrimSpace(string(out)) != filepath.Join(dir, "shot.png") {
		t.Fatalf("remote save failed: %q %v", out, err)
	}
	old := filepath.Join(legacy, "shot.png")
	keep := filepath.Join(legacy, "keep.png")
	for _, path := range []string{old, keep} {
		if err := os.WriteFile(path, []byte("legacy image"), 0640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0640); err != nil {
			t.Fatal(err)
		}
	}
	command := remoteScreenshotDeleteCommandAt("shot.png", old, legacy)
	if out, err := exec.Command("sh", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("legacy delete failed: %s %v", out, err)
	}
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Fatalf("legacy mirrored copy remains: %v", err)
	}
	for path, expected := range map[string]string{filepath.Join(dir, "shot.png"): "new image", keep: "legacy image"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != expected {
			t.Fatalf("unrequested collision/copy changed: %s %q %v", path, data, err)
		}
	}
}

func TestRemoteLegacyDeletionRejectsUnsafeTargetsAndArbitraryPaths(t *testing.T) {
	for _, unsafe := range []string{"file-symlink", "directory-symlink", "foreign", "writable", "outside-cache"} {
		t.Run(unsafe, func(t *testing.T) {
			root, _, legacy := remoteScreenshotFixture(t)
			victim := filepath.Join(root, "outside", "shot.png")
			if err := os.Mkdir(filepath.Dir(victim), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(victim, []byte("original"), 0644); err != nil {
				t.Fatal(err)
			}
			shot := filepath.Join(legacy, "shot.png")
			switch unsafe {
			case "file-symlink":
				if err := os.Symlink(victim, shot); err != nil {
					t.Fatal(err)
				}
			case "directory-symlink":
				if err := os.Remove(legacy); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(root, legacy); err != nil {
					t.Fatal(err)
				}
			case "outside-cache":
				shot = victim
			default:
				if err := os.WriteFile(shot, []byte("original"), 0644); err != nil {
					t.Fatal(err)
				}
				if unsafe == "foreign" {
					if err := os.Chown(shot, os.Getuid()+1, -1); err != nil {
						t.Skipf("foreign-owner fixture requires chown permission: %v", err)
					}
				} else if err := os.Chmod(shot, 0666); err != nil {
					t.Fatal(err)
				}
			}
			command := remoteScreenshotDeleteCommandAt("shot.png", shot, legacy)
			if exec.Command("sh", "-c", command).Run() == nil {
				t.Fatal("unsafe remote deletion succeeded")
			}
			data, err := os.ReadFile(victim)
			if err != nil || string(data) != "original" {
				t.Fatalf("remote victim changed: %q %v", data, err)
			}
		})
	}
}

func managedScreenshotFixture(t *testing.T, root string) *sshSession {
	t.Helper()
	// Substitute transport only; production shell commands mutate real files.
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\nfor argument do command=$argument; done\nexec /bin/sh -c \"$command\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	controlDir := filepath.Join(root, "control")
	if err := protocol.PrivateDir(controlDir); err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(controlDir, "master")
	listener, err := net.Listen("unix", control)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := os.Chmod(control, 0600); err != nil {
		t.Fatal(err)
	}
	session := &sshSession{ctx: context.Background(), controlPath: control}
	session.status.Host, session.status.State = "host-B", "connected"
	return session
}

func TestGalleryCleanupBindsHostAndDeletesOnlyRequestedRemoteRecords(t *testing.T) {
	root, remoteDir, _ := remoteScreenshotFixture(t)
	session := managedScreenshotFixture(t, root)
	for _, name := range []string{"same.png", "listed.png", "unlisted.png"} {
		payload, _ := json.Marshal(map[string]string{"filename": name, "base64": base64.StdEncoding.EncodeToString([]byte(name))})
		result, err := handleScreenshotSystem(context.Background(), session, "system_save_screenshot", payload)
		if err != nil || result.(map[string]any)["mirrored"] != true {
			t.Fatalf("remote capture not saved: %+v %v", result, err)
		}
	}
	original := filepath.Join(root, "host-A", "same.png")
	if err := os.Mkdir(filepath.Dir(original), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte("host-A image"), 0600); err != nil {
		t.Fatal(err)
	}
	deletePayload, _ := json.Marshal(screenshotRecord{Filename: "same.png", TargetHost: "host-A", RemotePath: original})
	deleted, err := handleScreenshotSystem(context.Background(), session, "system_delete_screenshot", deletePayload)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.(map[string]any)["remoteDeleted"] != false {
		t.Fatal("wrong-host Delete claimed success")
	}
	for path, expected := range map[string]string{original: "host-A image", filepath.Join(remoteDir, "same.png"): "same.png"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != expected {
			t.Fatalf("wrong-host Delete changed a remote copy: %s %q %v", path, data, err)
		}
	}
	clearPayload, _ := json.Marshal(map[string]any{"screenshots": []screenshotRecord{
		{Filename: "same.png", TargetHost: "host-A", RemotePath: original},
		{Filename: "listed.png", TargetHost: "host-B", RemotePath: filepath.Join(remoteDir, "listed.png")},
	}})
	cleared, err := handleScreenshotSystem(context.Background(), session, "system_clear_screenshots", clearPayload)
	if err != nil {
		t.Fatal(err)
	}
	status := cleared.(map[string]any)
	results := status["results"].([]map[string]any)
	if status["localDeleted"] != true || status["remoteDeleted"] != false || results[0]["remoteDeleted"] != false || results[1]["remoteDeleted"] != true {
		t.Fatalf("mixed-host Clear lost per-record failures: %+v", status)
	}
	if _, err := os.Lstat(filepath.Join(remoteDir, "listed.png")); !os.IsNotExist(err) {
		t.Fatalf("requested host-B image remains: %v", err)
	}
	for path, expected := range map[string]string{original: "host-A image", filepath.Join(remoteDir, "same.png"): "same.png", filepath.Join(remoteDir, "unlisted.png"): "unlisted.png"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != expected {
			t.Fatalf("Clear changed an unrequested remote copy: %s %q %v", path, data, err)
		}
	}
	entries, err := os.ReadDir(localScreenshotsDir())
	if err != nil || len(entries) != 0 {
		t.Fatalf("Clear left local images: %+v %v", entries, err)
	}
}

func TestConnectedClearWithoutGalleryProvenanceRefusesRemoteMutation(t *testing.T) {
	root, remoteDir, _ := remoteScreenshotFixture(t)
	if err := os.MkdirAll(remoteDir, 0700); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(remoteDir, "unlisted.png")
	if err := os.WriteFile(shot, []byte("remote image"), 0600); err != nil {
		t.Fatal(err)
	}
	session := managedScreenshotFixture(t, root)
	result, err := handleScreenshotSystem(context.Background(), session, "system_clear_screenshots", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	status := result.(map[string]any)
	if status["localDeleted"] != true || status["remoteDeleted"] != false || status["remoteError"] == nil {
		t.Fatalf("destination-free Clear claimed remote success: %+v", status)
	}
	data, err := os.ReadFile(shot)
	if err != nil || string(data) != "remote image" {
		t.Fatalf("destination-free Clear changed remote capture: %q %v", data, err)
	}
}
