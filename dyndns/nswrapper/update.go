package nswrapper

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/labstack/gommon/log"
	"io/ioutil"
	"os"
	"os/exec"
)

// UpdateRecord builds a nsupdate file and updates a record by executing it with nsupdate.
func UpdateRecord(hostname string, target string, addrType string, zone string, ttl int) error {
	log.Info(fmt.Sprintf("%s record update request: %s -> %s", addrType, hostname, target))

	f, err := ioutil.TempFile(os.TempDir(), "dyndns")
	if err != nil {
		return err
	}

	defer os.Remove(f.Name())
	w := bufio.NewWriter(f)

	w.WriteString(fmt.Sprintf("server %s\n", "localhost"))
	w.WriteString(fmt.Sprintf("zone %s\n", zone))
	w.WriteString(fmt.Sprintf("update delete %s.%s %s\n", hostname, zone, addrType))
	w.WriteString(fmt.Sprintf("update add %s.%s %v %s %s\n", hostname, zone, ttl, addrType, target))
	w.WriteString("send\n")

	w.Flush()
	f.Close()

	cmd := exec.Command("/usr/bin/nsupdate", f.Name())
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("%v: %v", err, stderr.String())
	}

	if out.String() != "" {
		return fmt.Errorf(out.String())
	}

	return nil
}

// DeleteRecord builds a nsupdate file and deletes a record by executing it with nsupdate.
func DeleteRecord(hostname string, zone string) error {
	fmt.Printf("record delete request: %s\n", hostname)

	f, err := ioutil.TempFile(os.TempDir(), "dyndns")
	if err != nil {
		return err
	}

	defer os.Remove(f.Name())
	w := bufio.NewWriter(f)

	w.WriteString(fmt.Sprintf("server %s\n", "localhost"))
	w.WriteString(fmt.Sprintf("zone %s\n", zone))
	w.WriteString(fmt.Sprintf("update delete %s.%s\n", hostname, zone))
	w.WriteString("send\n")

	w.Flush()
	f.Close()

	cmd := exec.Command("/usr/bin/nsupdate", f.Name())
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("%v: %v", err, stderr.String())
	}

	if out.String() != "" {
		return fmt.Errorf(out.String())
	}

	return nil
}
