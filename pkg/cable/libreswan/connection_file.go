/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

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

package libreswan

import (
	"os"
	goslices "slices"
	"strings"

	"github.com/pkg/errors"
)

type ConnectionFile struct {
	Path string
}

func (c *ConnectionFile) AppendConnectionStanza(stanza, connName string) error {
	if err := c.RemoveConnectionStanza(connName); err != nil {
		return err
	}

	f, err := os.OpenFile(c.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return errors.Wrapf(err, "error opening file %q", c.Path)
	}

	defer f.Close()

	if !strings.HasSuffix(stanza, "\n") {
		stanza += "\n"
	}

	_, err = f.WriteString(stanza)

	return errors.Wrapf(err, "error writing to file %q", c.Path)
}

func (c *ConnectionFile) RemoveConnectionStanza(connName string) error {
	data, err := os.ReadFile(c.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return errors.Wrapf(err, "error reading file %q", c.Path)
	}

	lines := strings.Split(string(data), "\n")

	lines = goslices.DeleteFunc(lines, func(s string) bool {
		return s == ""
	})

	start := goslices.IndexFunc(lines, func(s string) bool {
		return strings.TrimSpace(s) == ("conn " + connName)
	})

	if start == -1 {
		return nil
	}

	end := goslices.IndexFunc(lines[start+1:], func(s string) bool {
		return strings.HasPrefix(strings.TrimSpace(s), "conn ")
	})

	if end == -1 {
		end = len(lines)
	} else {
		end += start + 1
	}

	lines = goslices.Delete(lines, start, end)

	if len(lines) == 0 {
		return errors.Wrapf(os.Remove(c.Path), "error removing file %q", c.Path)
	}

	return errors.Wrapf(os.WriteFile(c.Path, []byte(strings.Join(lines, "\n")+"\n"), 0o600), "error writing file %q", c.Path)
}
