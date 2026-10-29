package nswrapper

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var labelPattern = regexp.MustCompile(`^[a-z0-9_](?:[a-z0-9_-]{0,61}[a-z0-9_])?$`)
var Types = []string{"A", "AAAA", "CNAME", "TXT", "MX", "SRV", "CAA", "NS"}

func ValidName(name string, wildcard bool) bool {
	if len(name) > 253 || name == "" {
		return false
	}
	for i, label := range strings.Split(name, ".") {
		if wildcard && i == 0 && label == "*" {
			continue
		}
		if !labelPattern.MatchString(label) {
			return false
		}
	}
	return true
}
func Owner(zone, name string) (string, error) {
	if !ValidName(zone, false) {
		return "", fmt.Errorf("invalid zone")
	}
	if name == "@" {
		return zone + ".", nil
	}
	if !ValidName(name, true) || len(name)+len(zone)+1 > 253 {
		return "", fmt.Errorf("invalid record name")
	}
	return name + "." + zone + ".", nil
}
func domainValue(value string) (string, error) {
	value = strings.TrimSuffix(strings.ToLower(value), ".")
	if !ValidName(value, false) || !strings.Contains(value, ".") || strings.Contains(value, "_") || net.ParseIP(value) != nil {
		return "", fmt.Errorf("target must be a DNS hostname")
	}
	return value + ".", nil
}
func quoted(s string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, b := range []byte(s) {
		if b < 32 || b > 126 {
			fmt.Fprintf(&out, "\\%03d", b)
		} else if b == '"' || b == '\\' {
			out.WriteByte('\\')
			out.WriteByte(b)
		} else {
			out.WriteByte(b)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// FormatRData validates all supported types and emits only DNS master-file syntax.
func FormatRData(kind, value string) (string, error) {
	bad := fmt.Errorf("invalid %s value", kind)
	switch kind {
	case "A", "AAAA":
		ip := net.ParseIP(value)
		if ip == nil || (kind == "A") != (ip.To4() != nil) {
			return "", bad
		}
		return ip.String(), nil
	case "CNAME", "NS":
		return domainValue(value)
	case "TXT":
		if len(value) > 65500 {
			return "", bad
		}
		var chunks []string
		for len(value) > 255 {
			chunks = append(chunks, quoted(value[:255]))
			value = value[255:]
		}
		chunks = append(chunks, quoted(value))
		return strings.Join(chunks, " "), nil
	case "MX", "SRV":
		fields := strings.Fields(value)
		n := 1
		if kind == "SRV" {
			n = 3
		}
		if len(fields) != n+1 {
			return "", bad
		}
		for i := 0; i < n; i++ {
			v, err := strconv.ParseUint(fields[i], 10, 16)
			if err != nil {
				return "", bad
			}
			fields[i] = strconv.FormatUint(v, 10)
		}
		if fields[n] == "." && (kind == "SRV" || (kind == "MX" && fields[0] == "0")) {
			return strings.Join(fields, " "), nil
		}
		target, err := domainValue(fields[n])
		if err != nil {
			return "", err
		}
		fields[n] = target
		return strings.Join(fields, " "), nil
	case "CAA":
		fields := strings.SplitN(value, " ", 3)
		if len(fields) != 3 || (fields[0] != "0" && fields[0] != "128") {
			return "", bad
		}
		if fields[1] != "issue" && fields[1] != "issuewild" && fields[1] != "iodef" {
			return "", bad
		}
		if len(fields[2]) > 255 {
			return "", bad
		}
		return fields[0] + " " + fields[1] + " " + quoted(fields[2]), nil
	}
	return "", fmt.Errorf("unsupported record type")
}

type RRset struct {
	Zone, Name, Type string
	TTL              int
	Values           []string
}

func RRScript(sets []RRset) (string, error) {
	if len(sets) == 0 {
		return "", fmt.Errorf("empty update")
	}
	zone := sets[0].Zone
	var out strings.Builder
	fmt.Fprintf(&out, "server localhost\nzone %s.\n", zone)
	for _, set := range sets {
		owner, err := Owner(set.Zone, set.Name)
		if err != nil {
			return "", err
		}
		if set.Zone != zone {
			return "", fmt.Errorf("updates must share a zone")
		}
		supported := false
		for _, kind := range Types {
			if set.Type == kind {
				supported = true
			}
		}
		if !supported {
			return "", fmt.Errorf("unsupported type")
		}
		if set.TTL < 20 || set.TTL > 86400 {
			return "", fmt.Errorf("TTL must be 20–86400")
		}
		if set.Name == "@" && (set.Type == "CNAME" || set.Type == "NS") {
			return "", fmt.Errorf("apex CNAME/NS is reserved")
		}
		if len(set.Values) > 0 {
			if set.Type == "CNAME" && len(set.Values) > 1 {
				return "", fmt.Errorf("CNAME permits one target")
			}
			if set.Type == "SRV" {
				parts := strings.Split(set.Name, ".")
				if len(parts) < 2 || !strings.HasPrefix(parts[0], "_") || !strings.HasPrefix(parts[1], "_") {
					return "", fmt.Errorf("SRV name must be _service._proto.label")
				}
			}
		}
		fmt.Fprintf(&out, "update delete %s %s\n", owner, set.Type)
		for _, value := range set.Values {
			rdata, err := FormatRData(set.Type, value)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&out, "update add %s %d %s %s\n", owner, set.TTL, set.Type, rdata)
		}
	}
	out.WriteString("send\n")
	return out.String(), nil
}
func ApplyBatch(sets []RRset) error {
	script, err := RRScript(sets)
	if err != nil {
		return err
	}
	return execute(script)
}
func execute(script string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/nsupdate")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("DNS update failed: %w", err)
	}
	if len(output) > 0 {
		return fmt.Errorf("DNS update rejected")
	}
	return nil
}
func ApplyRRset(zone, name, kind string, ttl int, values []string) error {
	return ApplyBatch([]RRset{{zone, name, kind, ttl, values}})
}
func DeleteRRset(zone, name, kind string) error { return ApplyRRset(zone, name, kind, 60, nil) }

// ChangeTXT adds or removes exactly one challenge value without replacing the RRset.
func ChangeTXT(zone, name, value string, add bool) error {
	owner, e := Owner(zone, name)
	if e != nil {
		return e
	}
	rdata, e := FormatRData("TXT", value)
	if e != nil {
		return e
	}
	operation := "delete " + owner + " TXT " + rdata
	if add {
		operation = "add " + owner + " 60 TXT " + rdata
	}
	return execute("server localhost\nzone " + zone + ".\nupdate " + operation + "\nsend\n")
}
