package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"ocf/internal/common"
	"strings"

	"github.com/multiformats/go-multiaddr"
	"github.com/spf13/viper"
)

var lookupTXT = net.LookupTXT

func getDefaultBootstrapPeers(bootstrapAddrs []string, mode string) []multiaddr.Multiaddr {
	if mode == "standalone" {
		common.Logger.Info("Bootstrap: []")
		return []multiaddr.Multiaddr{}
	}

	var sources []string
	switch {
	case bootstrapAddrs != nil:
		sources = append(sources, bootstrapAddrs...)
	case mode == "local":
		sources = []string{"/ip4/127.0.0.1/tcp/43905"}
	default:
		sources = collectBootstrapSources()
	}

	if len(sources) == 0 {
		common.Logger.Warn("No bootstrap sources configured")
		return []multiaddr.Multiaddr{}
	}

	resolved := resolveBootstrapSources(sources)
	peers := parseBootstrapMultiaddrs(resolved)
	if len(peers) == 0 {
		common.Logger.Warn("No bootstrap addresses discovered from configured sources")
		return nil
	}

	common.Logger.Info("Bootstrap: ", peers)
	return peers
}

func collectBootstrapSources() []string {
	var combined []string
	appendAll := func(values []string) {
		if len(values) == 0 {
			return
		}
		combined = append(combined, values...)
	}

	appendAll(viper.GetStringSlice("bootstrap.sources"))
	appendAll(viper.GetStringSlice("bootstrap.source"))
	appendAll(viper.GetStringSlice("bootstrap.static"))
	appendAll(viper.GetStringSlice("bootstrap.addrs"))

	if legacy := strings.TrimSpace(viper.GetString("bootstrap.addr")); legacy != "" {
		combined = append(combined, expandBootstrapValue(legacy)...)
	}

	combined = common.DeduplicateStrings(combined)
	return combined
}

func resolveBootstrapSources(sources []string) []string {
	var resolved []string
	for _, source := range sources {
		entries, err := resolveBootstrapSource(strings.TrimSpace(source))
		if err != nil {
			common.Logger.With("source", source).Warnf("Bootstrap source failed: %v", err)
			continue
		}
		if len(entries) == 0 {
			continue
		}
		resolved = append(resolved, entries...)
	}

	return common.DeduplicateStrings(resolved)
}

func resolveBootstrapSource(source string) ([]string, error) {
	if source == "" {
		return nil, nil
	}

	switch {
	case strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://"):
		return fetchHTTPBootstraps(source)
	case strings.HasPrefix(strings.ToLower(source), "dnsaddr://"):
		host := source[len("dnsaddr://"):]
		return fetchDNSAddrBootstraps(host)
	default:
		return expandBootstrapValue(source), nil
	}
}

func fetchHTTPBootstraps(url string) ([]string, error) {
	common.Logger.With("source", url).Info("Fetching bootstrap list")
	body, err := common.RemoteGET(url)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if len(body) == 0 {
		return nil, errors.New("empty response")
	}
	var payload common.Bootstraps
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid bootstrap payload: %w", err)
	}
	return payload.Bootstraps, nil
}

func fetchDNSAddrBootstraps(host string) ([]string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, errors.New("empty dnsaddr host")
	}
	records, err := lookupTXT("_dnsaddr." + host)
	if err != nil {
		return nil, fmt.Errorf("lookup failed: %w", err)
	}
	var addrs []string
	for _, record := range records {
		record = strings.TrimSpace(record)
		if !strings.HasPrefix(record, "dnsaddr=") {
			continue
		}
		addr := strings.TrimPrefix(record, "dnsaddr=")
		addr = strings.TrimSpace(addr)
		if addr != "" {
			addrs = append(addrs, addr)
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no dnsaddr records for %s", host)
	}
	return addrs, nil
}

func expandBootstrapValue(value string) []string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}

	if strings.HasPrefix(trimmed, "[") {
		var list []string
		if err := json.Unmarshal([]byte(trimmed), &list); err == nil {
			return list
		}
	}

	segments := splitBootstrapValue(trimmed)
	if len(segments) == 0 {
		return []string{trimmed}
	}
	return segments
}

func splitBootstrapValue(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
	})
	return fields
}

func parseBootstrapMultiaddrs(addrs []string) []multiaddr.Multiaddr {
	var result []multiaddr.Multiaddr
	seen := make(map[string]struct{})
	for _, addr := range addrs {
		addr = strings.TrimSpace(strings.Trim(addr, "\""))
		if addr == "" {
			continue
		}
		ma, err := multiaddr.NewMultiaddr(addr)
		if err != nil {
			common.Logger.With("addr", addr).Warnf("Skipping invalid bootstrap multiaddr: %v", err)
			continue
		}
		key := ma.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, ma)
	}
	return result
}
