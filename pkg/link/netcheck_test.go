package link

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pion/stun/v3"
)

func TestNetcheckChoosesFastestSuccessfulProbe(t *testing.T) {
	t.Parallel()

	slow := startTestSTUNServer(t, 40*time.Millisecond, nil)
	fast := startTestSTUNServer(t, 5*time.Millisecond, nil)

	result := Netcheck(context.Background(), []string{slow, fast})
	if !result.UDP {
		t.Fatal("expected UDP reachability")
	}
	if result.PublicAddr == "" {
		t.Fatal("expected public_addr")
	}
	if result.PreferredServer != fast {
		t.Fatalf("preferred_server = %q, want %q", result.PreferredServer, fast)
	}
	if len(result.Probes) != 2 {
		t.Fatalf("len(probes) = %d, want 2", len(result.Probes))
	}
}

func TestNetcheckDetectsMappingVariance(t *testing.T) {
	t.Parallel()

	addrA := &net.UDPAddr{IP: net.IPv4(203, 0, 113, 10), Port: 40000}
	addrB := &net.UDPAddr{IP: net.IPv4(203, 0, 113, 10), Port: 40001}
	serverA := startTestSTUNServer(t, 0, addrA)
	serverB := startTestSTUNServer(t, 0, addrB)

	result := Netcheck(context.Background(), []string{serverA, serverB})
	if !result.MappingVariesByServer {
		t.Fatal("expected mapping variance to be detected")
	}
}

func TestNetcheckReportsProbeErrors(t *testing.T) {
	t.Parallel()

	result := Netcheck(context.Background(), []string{"127.0.0.1:1"})
	if result.UDP {
		t.Fatal("expected UDP reachability to be false")
	}
	if len(result.Probes) != 1 {
		t.Fatalf("len(probes) = %d, want 1", len(result.Probes))
	}
	if result.Probes[0].Error == "" {
		t.Fatal("expected probe error")
	}
}

func startTestSTUNServer(t *testing.T, delay time.Duration, mapped *net.UDPAddr) string {
	t.Helper()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}

			req := &stun.Message{Raw: append([]byte(nil), buf[:n]...)}
			if err := req.Decode(); err != nil {
				continue
			}

			responseAddr := addr
			if mapped != nil {
				responseAddr = mapped
			}

			if delay > 0 {
				time.Sleep(delay)
			}

			resp := stun.MustBuild(
				stun.NewTransactionIDSetter(req.TransactionID),
				stun.BindingSuccess,
				&stun.XORMappedAddress{IP: responseAddr.IP, Port: responseAddr.Port},
			)
			_, _ = conn.WriteToUDP(resp.Raw, addr)
		}
	}()

	return conn.LocalAddr().String()
}
