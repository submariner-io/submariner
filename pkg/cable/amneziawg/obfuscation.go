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
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/advanced-wg/awgctrl-go/wgtypes"
	"github.com/pkg/errors"
)

// obfuscationOptionTable is the AmneziaWG obfuscation options and built-in defaults.
//
// AmneziaWG always applies these defaults (there is no "plain WireGuard" mode via options):
// the driver is intentionally an obfuscating tunnel, not a WG drop-in with optional junk.
//
// The vendored github.com/amnezia-vpn/amneziawg-go device only implements the subset of the
// AmneziaWG protocol below: magic headers (H1-H4) are single uint32 values, not "min-max"
// ranges, and there is no S3 (cookie) / S4 (transport) padding support. Sending either of
// those over the UAPI configuration protocol fails the whole "set" operation with EINVAL,
// which previously broke every fresh AmneziaWG gateway (see the issue link below).
//
// H1–H4 and S1–S2 MUST be identical on every Submariner peer (every cluster using this driver).
// Jc / Jmin / Jmax / I1–I5 may differ per side, but the shipped defaults keep both sides symmetric
// so a single cableDriverOptions map (or no overrides) works out of the box. Mismatched H*/S*
// typically yields silent connectivity failure — override carefully and keep clusters in sync.
//
// Report: https://github.com/submariner-io/submariner/issues/4118
func obfuscationOptionTable(cfg *wgtypes.Config) []cableDriverOption {
	return []cableDriverOption{
		// Junk packets before each handshake (helps break WG size signatures).
		option("jc", "7", parsePositiveInt, &cfg.Jc),
		option("jmin", "80", parsePositiveInt, &cfg.Jmin),
		option("jmax", "200", parsePositiveInt, &cfg.Jmax),

		// Message padding. Must match on every peer.
		option("s1", "45", parseNonNegativeInt, &cfg.S1),
		option("s2", "60", parseNonNegativeInt, &cfg.S2),

		// Magic headers (single uint32 per packet type) — not the fixed WG types 1–4.
		// Must match on every peer.
		option("h1", "240000000", parseHeader, &cfg.H1),
		option("h2", "440000000", parseHeader, &cfg.H2),
		option("h3", "640000000", parseHeader, &cfg.H3),
		option("h4", "840000000", parseHeader, &cfg.H4),

		// Custom init packets (AWG 2.0) — random-looking UDP before handshake.
		option("i1", "<r 40>", parseInitPacket, &cfg.I1),
		option("i2", "<r 25>", parseInitPacket, &cfg.I2),
		option("i3", "<r 20>", parseInitPacket, &cfg.I3),
		option("i4", "<r 15>", parseInitPacket, &cfg.I4),
		option("i5", "<r 15>", parseInitPacket, &cfg.I5),
	}
}

func validateObfuscationOptions(table []cableDriverOption) []error {
	var errs []error

	jmin, jminOK := optionValue[*int](table, "jmin")
	jmax, jmaxOK := optionValue[*int](table, "jmax")

	if jminOK && jmaxOK && jmin != nil && jmax != nil && *jmin > *jmax {
		errs = append(errs, errors.Errorf(
			"invalid AmneziaWG options: jmin (%d) must be <= jmax (%d)", *jmin, *jmax))
	}

	type namedHeader struct {
		name  string
		value uint64
	}

	headers := make([]namedHeader, 0, 4)

	for _, key := range []string{"h1", "h2", "h3", "h4"} {
		val, ok := optionValue[*string](table, key)
		if !ok || val == nil {
			continue
		}

		// Already validated by parseHeader; the error was reported there.
		n, err := strconv.ParseUint(*val, 10, 32)
		if err != nil {
			continue
		}

		headers = append(headers, namedHeader{name: key, value: n})
	}

	for i := range headers {
		for j := i + 1; j < len(headers); j++ {
			if headers[i].value == headers[j].value {
				errs = append(errs, errors.Errorf(
					"invalid AmneziaWG options: %s and %s must not have the same value (%d)",
					headers[i].name, headers[j].name, headers[i].value))
			}
		}
	}

	return errs
}

// parseHeader mirrors amneziawg-go's handleDeviceLine for h1-h4: a single uint32, not a range.
func parseHeader(key, value string) (*string, error) {
	if _, err := strconv.ParseUint(value, 10, 32); err != nil {
		return nil, errors.Wrapf(err, "invalid AmneziaWG option %q=%q: must be a single uint32", key, value)
	}

	return new(value), nil
}

func parseInitPacket(key, value string) (*string, error) {
	if err := validateInitPacketSpec(value); err != nil {
		return nil, errors.Wrapf(err, "invalid AmneziaWG option %q=%q", key, value)
	}

	return new(value), nil
}

// validateInitPacketSpec mirrors amneziawg-go newObfChain tag grammar closely enough to fail fast.
func validateInitPacketSpec(spec string) error {
	remaining := spec
	tagCount := 0

	for {
		start := strings.IndexByte(remaining, '<')
		if start == -1 {
			break
		}

		endRel := strings.IndexByte(remaining[start:], '>')
		if endRel == -1 {
			return errors.New("missing enclosing '>'")
		}

		end := start + endRel
		tag := remaining[start+1 : end]
		parts := strings.Fields(tag)

		if len(parts) == 0 {
			return errors.New("empty tag")
		}

		param := ""
		if len(parts) > 1 {
			param = parts[1]
		}

		if err := validateInitPacketTag(parts[0], param); err != nil {
			return err
		}

		tagCount++
		remaining = remaining[end+1:]
	}

	if tagCount == 0 {
		return errors.New("expected one or more <tag ...> values")
	}

	return nil
}

func validateInitPacketTag(name, param string) error {
	switch name {
	case "b":
		hexStr := strings.TrimPrefix(strings.TrimPrefix(param, "0x"), "0X")
		if hexStr == "" {
			return errors.Errorf("tag <b> requires hex bytes")
		}

		if len(hexStr)%2 != 0 {
			return errors.Errorf("tag <b> hex must have even length")
		}

		if _, err := hex.DecodeString(hexStr); err != nil {
			return errors.Wrap(err, "tag <b> invalid hex")
		}

		return nil

	case "r", "rc", "rd":
		n, err := strconv.Atoi(param)
		if err != nil {
			return errors.Wrapf(err, "tag <%s> requires an integer size", name)
		}

		if n < 0 {
			return errors.Errorf("tag <%s> size must be >= 0", name)
		}

		return nil

	case "t", "d", "ds", "dz":
		// amneziawg-go accepts these with an ignored/empty parameter.
		return nil

	default:
		return errors.Errorf("unknown tag <%s>", name)
	}
}
