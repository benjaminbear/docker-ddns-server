package nswrapper

import (
	"fmt"
	"os/exec"
	"strings"
)

// UpdateRecord builds a nsupdate file and updates a record by executing it with nsupdate.
func UpdateRecord(hostname string, target string, addrType string, zone string, ttl int, enableWildcard bool) error {
	sets := []RRset{{Zone: zone, Name: hostname, Type: addrType, TTL: ttl, Values: []string{target}}}
	if enableWildcard {
		sets = append(sets, RRset{Zone: zone, Name: "*." + hostname, Type: addrType, TTL: ttl, Values: []string{target}})
	}
	return ApplyBatch(sets)
}

// DeleteRecord deletes a name and optionally its wildcard.
func DeleteRecord(hostname string, zone string, enableWildcard bool) error {
	return DeleteRecords([]string{hostname}, zone, enableWildcard)
}

// DeleteRecords removes related names in one atomic, single-zone DNS update.
func DeleteRecords(names []string, zone string, enableWildcard bool) error {
	script, err := deleteScript(names, zone, enableWildcard)
	if err != nil {
		return err
	}
	cmd := exec.Command("/usr/bin/nsupdate")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nsupdate: %w: %s", err, output)
	}
	if len(output) != 0 {
		return fmt.Errorf("nsupdate: %s", output)
	}
	return nil
}

func deleteScript(names []string, zone string, wildcard bool) (string, error) {
	valid := func(s string) bool { return s != "" && !strings.ContainsAny(s, " \t\r\n;\\\"") }
	if !valid(zone) {
		return "", fmt.Errorf("invalid zone")
	}
	var script strings.Builder
	fmt.Fprintf(&script, "server localhost\nzone %s\n", zone)
	for _, name := range names {
		if !valid(name) {
			return "", fmt.Errorf("invalid name")
		}
		for _, kind := range []string{"A", "AAAA", "CNAME"} {
			fmt.Fprintf(&script, "update delete %s.%s %s\n", name, zone, kind)
		}
		if wildcard {
			for _, kind := range []string{"A", "AAAA", "CNAME"} {
				fmt.Fprintf(&script, "update delete *.%s.%s %s\n", name, zone, kind)
			}
		}
	}
	script.WriteString("send\n")
	return script.String(), nil
}
