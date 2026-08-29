// ldapi://（unix socket）路径验证：dial.go 对 ldapi scheme 原样透传
// go-ldap DialURL（conn.go:162-167：u.Path 为空时兜底 /var/run/slapd/ldapi，
// 否则按 URL Path 作为 socket 路径拨 AF_UNIX）。用本地假 unix 监听验证
// 「scheme 解析 → unix dial 成功 → bind 在 socket 上发出」，不依赖真实 slapd。
package ldapconn

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDialProfileLdapiUnixSocket(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "ldapi")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer listener.Close()

	accepted := make(chan struct{}, 1)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			// 假 slapd：只收不发，bind 等待响应直到超时。
			go func() {
				buf := make([]byte, 4096)
				for {
					if _, err := conn.Read(buf); err != nil {
						return
					}
				}
			}()
		}
	}()

	// ldapi:/// 格式：三斜线 + 绝对 socket 路径（url.Parse 落在 u.Path）。
	socketURL := (&url.URL{Scheme: "ldapi", Path: socketPath}).String()
	profile := NormalizeProfile(Profile{
		ID:             "ldapi-test",
		Name:           "ldapi",
		URL:            socketURL,
		AuthType:       LDAPAuthExternal,
		TimeoutSeconds: 1,
	})

	done := make(chan error, 1)
	go func() {
		_, dialErr := dialProfile(context.Background(), profile, connTarget{Host: "", Port: 0}, bindSecrets{})
		done <- dialErr
	}()

	select {
	case dialErr := <-done:
		// 关键断言 1：unix socket 被 dial（假 slapd 收到连接）。
		select {
		case <-accepted:
		default:
			t.Fatal("unix socket was never dialed")
		}
		// 关键断言 2：bind 阶段在 socket 上等待响应超时（ExternalBind 已发出），
		// 而不是 scheme/host 类错误。
		msg := dialErr.Error()
		if !strings.Contains(msg, "bind ldap") {
			t.Fatalf("dial error = %v, want bind-stage failure over unix socket", dialErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("dialProfile timed out")
	}

	// ldapi 缺省 socket 路径兜底语义（go-ldap conn.go:163-165）：
	// u.Path 为空或 "/" 时用 /var/run/slapd/ldapi，此处仅固定文档化该行为。
	if _, err := os.Stat("/var/run/slapd/ldapi"); err == nil {
		t.Log("/var/run/slapd/ldapi exists on this host; default-path dial would proceed")
	}
}
