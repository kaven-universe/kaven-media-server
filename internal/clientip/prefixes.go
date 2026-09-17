package clientip

import (
	"fmt"
	"net/netip"
)

const MaxTrustedPrefixes = 64

func ParseTrustedPrefixes(values []string) ([]netip.Prefix, error) {
	if len(values) > MaxTrustedPrefixes {
		return nil, fmt.Errorf("trustedProxyCIDRs supports at most %d entries", MaxTrustedPrefixes)
	}
	result := make([]netip.Prefix, 0, len(values))
	seen := make(map[netip.Prefix]struct{}, len(values))
	for index, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("trustedProxyCIDRs[%d] must be an IPv4 or IPv6 CIDR", index)
		}
		prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits())
		if !prefix.IsValid() || prefix != prefix.Masked() {
			return nil, fmt.Errorf("trustedProxyCIDRs[%d] must use a canonical network address", index)
		}
		if prefix.Bits() == 0 {
			return nil, fmt.Errorf("trustedProxyCIDRs[%d] must not trust the entire address space", index)
		}
		if _, exists := seen[prefix]; exists {
			return nil, fmt.Errorf("trustedProxyCIDRs contains duplicate %q", value)
		}
		seen[prefix] = struct{}{}
		result = append(result, prefix)
	}
	return result, nil
}
