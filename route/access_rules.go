package route

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
)

const (
	ipAllowTag = "allow:ip"
	ipDenyTag  = "deny:ip"
)

// AccessDeniedHTTP checks rules on the target for HTTP proxy routes.
func (t *Target) AccessDeniedHTTP(r *http.Request) bool {
	var ip net.IP
	host, _, err := net.SplitHostPort(r.RemoteAddr)

	if err != nil {
		log.Printf("[ERROR] failed to get host from remote header %s: %s",
			r.RemoteAddr, err.Error())
		return false
	}

	if ip = net.ParseIP(host); ip == nil {
		log.Printf("[WARN] failed to parse remote address %s", host)
	}

	// check xff source if present
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// only use left-most element (client)
		xff = strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
		// only continue if xff differs from host
		if xff != host {
			if ip = net.ParseIP(xff); ip == nil {
				log.Printf("[WARN] failed to parse xff address %s", xff)
			}
			// check xff source and return if denied
			if t.denyByIP(ip) {
				return true
			}
		}
	}

	// check remote source and return if denied
	if t.denyByIP(ip) {
		return true
	}

	// default allow
	return false
}

// AccessDeniedTCP checks rules on the target for TCP proxy routes.
func (t *Target) AccessDeniedTCP(c net.Conn) bool {
	ip := net.ParseIP(c.RemoteAddr().String())
	if t.denyByIP(ip) {
		return true
	}
	// default allow
	return false
}

func (t *Target) denyByIP(ip net.IP) bool {
	if ip == nil || t.accessRules == nil {
		return false
	}

	// check allow (whitelist) first if it exists
	if _, ok := t.accessRules[ipAllowTag]; ok {
		var block *net.IPNet
		for _, x := range t.accessRules[ipAllowTag] {
			if block, ok = x.(*net.IPNet); !ok {
				log.Print("[ERROR] failed to assert ip block while checking allow rule for ", t.Service)
				continue
			}
			if block.Contains(ip) {
				// specific allow matched - allow this request
				return false
			}
		}
		// we checked all the blocks - deny this request
		log.Printf("[INFO] route rules denied access from %s to %s",
			ip.String(), t.URL.String())
		return true
	}

	// still going - check deny (blacklist) if it exists
	if _, ok := t.accessRules[ipDenyTag]; ok {
		var block *net.IPNet
		for _, x := range t.accessRules[ipDenyTag] {
			if block, ok = x.(*net.IPNet); !ok {
				log.Print("[INFO] failed to assert ip block while checking deny rule for ", t.Service)
				continue
			}
			if block.Contains(ip) {
				// specific deny matched - deny this request
				log.Printf("[INFO] route rules denied access from %s to %s",
					ip.String(), t.URL.String())
				return true
			}
		}
	}

	// default - do not deny
	return false
}

// ProcessAccessRules processes access rules from options specified on the target route
func (t *Target) ProcessAccessRules() error {
	if t.Opts["allow"] != "" && t.Opts["deny"] != "" {
		return errors.New("specifying allow and deny on the same route is not supported")
	}

	for _, allowDeny := range []string{"allow", "deny"} {
		if t.Opts[allowDeny] != "" {
			if err := t.parseAccessRule(allowDeny); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *Target) parseAccessRule(allowDeny string) error {
	var accessTag string
	var temps []string
	var value string
	var ip net.IP

	// init rules if needed
	if t.accessRules == nil {
		t.accessRules = make(map[string][]interface{})
	}

	// loop over rule elements
	for _, c := range strings.Split(t.Opts[allowDeny], ",") {
		if temps = strings.SplitN(c, ":", 2); len(temps) != 2 {
			return fmt.Errorf("invalid access item, expected <type>:<data>, got %s", temps)
		}

		// form access type tag
		accessTag = allowDeny + ":" + strings.ToLower(strings.TrimSpace(temps[0]))

		// switch on formed access tag - currently only ip types are implemented
		switch accessTag {
		case ipAllowTag, ipDenyTag:
			if value = strings.TrimSpace(temps[1]); !strings.Contains(value, "/") {
				if ip = net.ParseIP(value); ip == nil {
					return fmt.Errorf("failed to parse IP %s", value)
				}
				if ip.To4() != nil {
					value = ip.String() + "/32"
				} else {
					value = ip.String() + "/128"
				}
			}
			_, net, err := net.ParseCIDR(value)
			if err != nil {
				return fmt.Errorf("failed to parse CIDR %s with error: %s",
					c, err.Error())
			}
			// add element to rule map
			t.accessRules[accessTag] = append(t.accessRules[accessTag], net)
		default:
			return fmt.Errorf("unknown access item type: %s", temps[0])
		}
	}

	return nil
}
