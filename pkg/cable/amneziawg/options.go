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

package amneziawg

import (
	stderrors "errors"
	"sort"
	"strconv"
	"time"

	"github.com/advanced-wg/awgctrl-go/wgtypes"
	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/cable"
)

// cableDriverOption is one SUBMARINER_CABLEDRIVEROPTIONS key.
type cableDriverOption struct {
	key   string                               // JSON option name (e.g. "jc", "h1", "keepalive")
	def   string                               // default when the key is absent or empty in the env map
	dest  any                                  // *T — filled by parse, then read back for cross-field validation
	parse func(key, value string) (any, error) // parses value, stores into dest, returns the parsed value
}

// cableDriverOptionTable is the full set of keys accepted in cableDriverOptions.
func cableDriverOptionTable(a *amneziawgDriver, cfg *wgtypes.Config) []cableDriverOption {
	return append(obfuscationOptionTable(cfg), driverOptionTable(a)...)
}

// driverOptionTable holds non-obfuscation driver knobs from cableDriverOptions.
func driverOptionTable(a *amneziawgDriver) []cableDriverOption {
	return []cableDriverOption{
		// Persistent keepalive for peers (seconds). 0 disables; useful behind strict NAT.
		option("keepalive", "10", parseKeepAliveSeconds, &a.keepAlive),
	}
}

// option binds a JSON key to dest. parse validates and converts the string value.
func option[T any](key, def string, parse func(key, value string) (T, error), dest *T) cableDriverOption {
	return cableDriverOption{
		key:  key,
		def:  def,
		dest: dest,
		parse: func(key, value string) (any, error) {
			v, err := parse(key, value)
			if err != nil {
				return nil, err
			}

			*dest = v

			return v, nil
		},
	}
}

func (o *cableDriverOption) set(value string) error {
	_, err := o.parse(o.key, value)

	return err
}

func applyCableDriverOptions(a *amneziawgDriver, cfg *wgtypes.Config) error {
	options, err := cable.GetDriverOptions()
	if err != nil {
		return errors.Wrap(err, "error reading cable driver options")
	}

	return applyCableDriverOptionsMap(a, cfg, options)
}

// applyCableDriverOptionsMap applies obfuscation + driver option tables.
// Configuration problems are collected and returned together for a single fix pass.
func applyCableDriverOptionsMap(a *amneziawgDriver, cfg *wgtypes.Config, options map[string]string) error {
	table := cableDriverOptionTable(a, cfg)

	known := make(map[string]struct{}, len(table))
	for _, opt := range table {
		known[opt.key] = struct{}{}
	}

	var errs []error

	unknown := make([]string, 0)

	for key := range options {
		if _, ok := known[key]; !ok {
			unknown = append(unknown, key)
		}
	}

	sort.Strings(unknown)

	for _, key := range unknown {
		errs = append(errs, errors.Errorf("unknown AmneziaWG option %q", key))
	}

	for i := range table {
		value := table[i].def
		if v, ok := options[table[i].key]; ok && v != "" {
			value = v
		}

		if err := table[i].set(value); err != nil {
			errs = append(errs, err)
		}
	}

	errs = append(errs, validateObfuscationOptions(table)...)

	return stderrors.Join(errs...)
}

func optionValue[T any](table []cableDriverOption, key string) (T, bool) {
	var zero T

	for i := range table {
		if table[i].key != key {
			continue
		}

		dest, ok := table[i].dest.(*T)
		if !ok || dest == nil {
			return zero, false
		}

		return *dest, true
	}

	return zero, false
}

func parsePositiveInt(key, value string) (*int, error) {
	n, err := strconv.Atoi(value)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid AmneziaWG option %q=%q", key, value)
	}

	if n <= 0 {
		return nil, errors.Errorf("invalid AmneziaWG option %q=%q: must be > 0", key, value)
	}

	return new(n), nil
}

func parseNonNegativeInt(key, value string) (*int, error) {
	n, err := strconv.Atoi(value)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid AmneziaWG option %q=%q", key, value)
	}

	if n < 0 {
		return nil, errors.Errorf("invalid AmneziaWG option %q=%q: must be >= 0", key, value)
	}

	return new(n), nil
}

// WireGuard persistent-keepalive is stored as seconds in a uint16-sized field.
const maxKeepAliveSeconds = 65535

func parseKeepAliveSeconds(key, value string) (time.Duration, error) {
	sec, err := strconv.Atoi(value)
	if err != nil {
		return 0, errors.Wrapf(err, "invalid AmneziaWG option %q=%q", key, value)
	}

	if sec < 0 || sec > maxKeepAliveSeconds {
		return 0, errors.Errorf(
			"invalid AmneziaWG option %q=%q: must be between 0 and %d seconds", key, value, maxKeepAliveSeconds)
	}

	return time.Duration(sec) * time.Second, nil
}
