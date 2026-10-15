package plugin

import (
	"context"
	"errors"
	"fmt"
	"github.com/remnawave/node-go/internal/updater"
	"log"
	"net/netip"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	TableName               = "remnanode"
	TorrentBlockerSetName   = "torrent-blocker"
	IngressFilterIPSetName  = "ingress-filter-ip"
	EgressFilterIPSetName   = "egress-filter-ip"
	EgressFilterPortSetName = "egress-filter-port"
)

type NftManager struct {
	mu                 sync.Mutex
	available          bool
	logging            bool
	acceptReplyTraffic bool
	run                func(context.Context, string) error
}

func NewNftManager(logging, acceptReplyTraffic bool) *NftManager {
	m := &NftManager{logging: logging, acceptReplyTraffic: acceptReplyTraffic}
	if !updater.HasNetAdmin() {
		return m
	}
	path, err := exec.LookPath("nft")
	if err != nil {
		log.Println("[PLUGIN] nft unavailable")
		return m
	}
	m.run = func(ctx context.Context, script string) error {
		cmd := exec.CommandContext(ctx, path, "-f", "-")
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("nft: %w: %s", err, out)
		}
		return nil
	}
	m.available = true
	if err = m.RecreateTables(); err != nil {
		m.available = false
		log.Printf("[PLUGIN] nft initialization failed: %v", err)
	}
	return m
}
func (m *NftManager) IsAvailable() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.available }
func (m *NftManager) execute(script string) error {
	if !m.available || m.run == nil {
		return errors.New("nftables unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return m.run(ctx, script+"\n")
}
func (m *NftManager) tableScript() string {
	// All operations run in one kernel transaction, including replacement.
	lines := []string{"add table inet " + TableName, "delete table inet " + TableName, "add table inet " + TableName}
	for _, item := range []struct{ name, kind, flags string }{
		{TorrentBlockerSetName, "ipv4_addr", "timeout"}, {TorrentBlockerSetName + "-v6", "ipv6_addr", "timeout"},
		{IngressFilterIPSetName, "ipv4_addr", "interval; auto-merge"}, {IngressFilterIPSetName + "-v6", "ipv6_addr", "interval; auto-merge"},
		{EgressFilterIPSetName, "ipv4_addr", "interval; auto-merge"}, {EgressFilterIPSetName + "-v6", "ipv6_addr", "interval; auto-merge"},
		{EgressFilterPortSetName, "inet_service", ""},
	} {
		flags := ""
		if item.flags != "" {
			flags = "flags " + item.flags + ";"
		}
		lines = append(lines, fmt.Sprintf("add set inet %s %s { type %s; %s }", TableName, item.name, item.kind, flags))
	}
	lines = append(lines, "add chain inet "+TableName+" prerouting { type filter hook prerouting priority -150; policy accept; }",
		"add chain inet "+TableName+" output { type filter hook output priority 0; policy accept; }")
	if m.acceptReplyTraffic {
		lines = append(lines, "add rule inet "+TableName+" prerouting ct direction reply accept")
	}
	drop := "counter drop"
	if m.logging {
		drop = "log prefix \"remnanode \" " + drop
	}
	for _, set := range []string{IngressFilterIPSetName, TorrentBlockerSetName} {
		lines = append(lines, fmt.Sprintf("add rule inet %s prerouting ip saddr @%s %s", TableName, set, drop), fmt.Sprintf("add rule inet %s prerouting ip6 saddr @%s-v6 %s", TableName, set, drop))
	}
	lines = append(lines, fmt.Sprintf("add rule inet %s output ip daddr @%s %s", TableName, EgressFilterIPSetName, drop), fmt.Sprintf("add rule inet %s output ip6 daddr @%s-v6 %s", TableName, EgressFilterIPSetName, drop), fmt.Sprintf("add rule inet %s output meta l4proto { tcp, udp } th dport @%s %s", TableName, EgressFilterPortSetName, drop))
	return strings.Join(lines, "\n")
}
func validateAddress(s string) (string, bool, error) {
	if a, err := netip.ParseAddr(s); err == nil {
		return a.String(), a.Is6(), nil
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked().String(), p.Addr().Is6(), nil
	}
	return "", false, fmt.Errorf("invalid IP address or prefix %q", s)
}
func addressCommands(ips []string, set, operation string) (string, error) {
	if set != IngressFilterIPSetName && set != EgressFilterIPSetName && set != TorrentBlockerSetName {
		return "", errors.New("invalid nftables set")
	}
	groups := map[bool][]string{}
	for _, ip := range ips {
		v, v6, err := validateAddress(ip)
		if err != nil {
			return "", err
		}
		groups[v6] = append(groups[v6], v)
	}
	var lines []string
	for _, v6 := range []bool{false, true} {
		if len(groups[v6]) > 0 {
			name := set
			if v6 {
				name += "-v6"
			}
			lines = append(lines, fmt.Sprintf("%s element inet %s %s { %s }", operation, TableName, name, strings.Join(groups[v6], ", ")))
		}
	}
	return strings.Join(lines, "\n"), nil
}
func portCommand(ports []int, set string) (string, error) {
	if set != EgressFilterPortSetName {
		return "", errors.New("invalid port set")
	}
	var values []string
	for _, p := range ports {
		if p < 1 || p > 65535 {
			return "", fmt.Errorf("invalid port %d", p)
		}
		values = append(values, fmt.Sprint(p))
	}
	if len(values) == 0 {
		return "", nil
	}
	return fmt.Sprintf("add element inet %s %s { %s }", TableName, set, strings.Join(values, ", ")), nil
}
func (m *NftManager) SyncFilters(ingress, egress []string, ports []int) error {
	a, err := addressCommands(ingress, IngressFilterIPSetName, "add")
	if err != nil {
		return err
	}
	b, err := addressCommands(egress, EgressFilterIPSetName, "add")
	if err != nil {
		return err
	}
	c, err := portCommand(ports, EgressFilterPortSetName)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.execute(m.tableScript() + "\n" + a + "\n" + b + "\n" + c)
}
func (m *NftManager) RecreateTables() error { return m.SyncFilters(nil, nil, nil) }
func (m *NftManager) DeleteTable() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.execute("delete table inet " + TableName)
}
func (m *NftManager) BlockIP(ip string, timeout int) error {
	a, err := netip.ParseAddr(ip)
	if err != nil || timeout < 0 {
		return errors.New("invalid block address or timeout")
	}
	set := TorrentBlockerSetName
	if a.Is6() {
		set += "-v6"
	}
	v := a.String()
	if timeout > 0 {
		v += fmt.Sprintf(" timeout %ds", timeout)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.execute(fmt.Sprintf("add element inet %s %s { %s }", TableName, set, v))
}
func (m *NftManager) UnblockIPs(ips []string, set string) error {
	script, err := addressCommands(ips, set, "delete")
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.execute(script)
}
func (m *NftManager) AddAddresses(ips []string, set string) error {
	script, err := addressCommands(ips, set, "add")
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.execute(script)
}
func (m *NftManager) AddPorts(ports []int, set string) error {
	script, err := portCommand(ports, set)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.execute(script)
}

func KillSockets(ips []string) error {
	var result error
	for _, ip := range ips {
		a, err := netip.ParseAddr(ip)
		if err != nil {
			return errors.New("invalid connection-drop IP")
		}
		for _, direction := range []string{"dst", "src"} {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			out, err := exec.CommandContext(ctx, "ss", "-K", direction, a.String()).CombinedOutput()
			cancel()
			if err != nil {
				result = errors.Join(result, fmt.Errorf("ss: %w: %s", err, out))
			}
		}
	}
	return result
}
