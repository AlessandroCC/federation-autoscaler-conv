/*
Copyright 2026 Politecnico di Torino - NetGroup.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package testlib

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// ProviderDelayEntry pairs a provider's Docker IP with its simulated delay.
type ProviderDelayEntry struct {
	ProviderIP string
	DelayMs    int
	Label      string // e.g. "provider-1", for logging
}

// TCConsumerDelay manages per-provider netem delays on a consumer's Docker
// container. Each provider gets its own HTB class with a child netem qdisc,
// matched by a u32 filter on dst IP + UDP dport 30100. Traffic to providers
// not in the list flows through the default class undelayed.
type TCConsumerDelay struct {
	ContainerName  string
	Interface      string
	ProviderDelays []ProviderDelayEntry
}

// Apply installs the HTB + per-provider netem tree on the consumer container.
func (t *TCConsumerDelay) Apply() error {
	cmds := [][]string{
		{"tc", "qdisc", "add", "dev", t.Interface, "root", "handle", "42:", "htb", "default", "1"},
		{"tc", "class", "add", "dev", t.Interface, "parent", "42:", "classid", "42:1", "htb", "rate", "10gbit"},
	}

	for i, pd := range t.ProviderDelays {
		classID := i + 2
		netemHandle := 100 + i
		cmds = append(cmds,
			[]string{"tc", "class", "add", "dev", t.Interface, "parent", "42:", "classid",
				fmt.Sprintf("42:%d", classID), "htb", "rate", "10gbit"},
			[]string{"tc", "qdisc", "add", "dev", t.Interface, "parent",
				fmt.Sprintf("42:%d", classID), "handle", fmt.Sprintf("%d:", netemHandle),
				"netem", "delay", fmt.Sprintf("%dms", pd.DelayMs)},
			[]string{"tc", "filter", "add", "dev", t.Interface, "parent", "42:",
				"protocol", "ip", "prio", fmt.Sprintf("%d", i+1), "u32",
				"match", "ip", "dst", fmt.Sprintf("%s/32", pd.ProviderIP),
				"match", "ip", "protocol", "17", "0xff",
				"match", "ip", "dport", "30100", "0xffff",
				"flowid", fmt.Sprintf("42:%d", classID)},
		)
	}

	log.Printf("[tc-consumer] applying per-provider delays on %s (iface=%s)", t.ContainerName, t.Interface)
	for _, pd := range t.ProviderDelays {
		log.Printf("[tc-consumer]   → %s (%s): +%dms", pd.Label, pd.ProviderIP, pd.DelayMs)
	}

	for _, cmd := range cmds {
		if err := t.dockerExec(cmd...); err != nil {
			_ = t.Restore()
			return fmt.Errorf("tc command %v: %w", cmd, err)
		}
	}
	return nil
}

// UpdateDelays changes the netem delay on each per-provider qdisc in place.
func (t *TCConsumerDelay) UpdateDelays(newDelays []ProviderDelayEntry) error {
	for i, pd := range newDelays {
		if i >= len(t.ProviderDelays) {
			break
		}
		netemHandle := 100 + i
		if err := t.dockerExec("tc", "qdisc", "change", "dev", t.Interface, "parent",
			fmt.Sprintf("42:%d", i+2), "handle", fmt.Sprintf("%d:", netemHandle),
			"netem", "delay", fmt.Sprintf("%dms", pd.DelayMs)); err != nil {
			return fmt.Errorf("update delay on class 42:%d: %w", i+2, err)
		}
		t.ProviderDelays[i].DelayMs = pd.DelayMs
	}
	return nil
}

// Restore removes the handle 42: qdisc tree.
func (t *TCConsumerDelay) Restore() error {
	err := t.dockerExec("tc", "qdisc", "del", "dev", t.Interface, "root", "handle", "42:")
	if err != nil && !strings.Contains(err.Error(), "No such file or directory") &&
		!strings.Contains(err.Error(), "RTNETLINK answers: Invalid argument") {
		return fmt.Errorf("remove handle 42: %w", err)
	}
	return nil
}

func (t *TCConsumerDelay) dockerExec(args ...string) error {
	cmdArgs := append([]string{"exec", t.ContainerName}, args...)
	cmd := exec.Command("docker", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
