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
// H1–H4 and S1–S4 MUST be identical on every Submariner peer (every cluster using this driver).
// Jc / Jmin / Jmax / I1–I5 may differ per side, but the shipped defaults keep both sides symmetric
// so a single cableDriverOptions map (or no overrides) works out of the box. Mismatched H*/S*
// typically yields silent connectivity failure — override carefully and keep clusters in sync.
func obfuscationOptionTable(cfg *wgtypes.Config) []cableDriverOption {
	return []cableDriverOption{
		// Junk packets before each handshake (helps break WG size signatures).
		option("jc", "7", parsePositiveInt, &cfg.Jc),
		option("jmin", "80", parsePositiveInt, &cfg.Jmin),
		option("jmax", "200", parsePositiveInt, &cfg.Jmax),

		// Message padding (AWG 2+/3: S3 cookie, S4 transport). Must match on every peer.
		// S4 default is 12 so optional AWG 3 header protection (nonce size) can be enabled later.
		option("s1", "45", parseNonNegativeInt, &cfg.S1),
		option("s2", "60", parseNonNegativeInt, &cfg.S2),
		option("s3", "35", parseNonNegativeInt, &cfg.S3),
		option("s4", "12", parseNonNegativeInt, &cfg.S4),

		// Magic headers as non-overlapping ranges (AWG 2+/3) — not the fixed WG types 1–4.
		// Must match on every peer.
		option("h1", "200000000-280000000", parseHeader, &cfg.H1),
		option("h2", "400000000-480000000", parseHeader, &cfg.H2),
		option("h3", "600000000-680000000", parseHeader, &cfg.H3),
		option("h4", "800000000-880000000", parseHeader, &cfg.H4),

		// Custom init packets (AWG 2+/3) — random-looking UDP before handshake.
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

	type namedRange struct {
		name  string
		value string
		r     headerRange
	}

	headers := make([]namedRange, 0, 4)

	for _, key := range []string{"h1", "h2", "h3", "h4"} {
		val, ok := optionValue[*string](table, key)
		if !ok || val == nil {
			continue
		}

		r, err := parseHeaderRange(*val)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "invalid AmneziaWG option %q", key))
			continue
		}

		headers = append(headers, namedRange{name: key, value: *val, r: r})
	}

	for i := range headers {
		for j := i + 1; j < len(headers); j++ {
			left, right := headers[i], headers[j]
			if left.r.start <= right.r.end && right.r.start <= left.r.end {
				errs = append(errs, errors.Errorf(
					"invalid AmneziaWG options: %s (%s) overlaps %s (%s)",
					left.name, left.value, right.name, right.value))
			}
		}
	}

	return errs
}

type headerRange struct {
	start uint64
	end   uint64
}

func parseHeader(key, value string) (*string, error) {
	if _, err := parseHeaderRange(value); err != nil {
		return nil, errors.Wrapf(err, "invalid AmneziaWG option %q=%q", key, value)
	}

	return new(value), nil
}

// parseHeaderRange mirrors amneziawg-go/v3 UintRange.FromString: single uint32 or min-max with min <= max.
func parseHeaderRange(value string) (headerRange, error) {
	parts := strings.Split(value, "-")
	if len(parts) < 1 || len(parts) > 2 {
		return headerRange{}, errors.New("expected uint32 or min-max range")
	}

	start, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return headerRange{}, errors.Wrap(err, "invalid range start")
	}

	end := start
	if len(parts) == 2 {
		end, err = strconv.ParseUint(parts[1], 10, 32)
		if err != nil {
			return headerRange{}, errors.Wrap(err, "invalid range end")
		}
	}

	if end < start {
		return headerRange{}, errors.New("range start must be <= end")
	}

	return headerRange{start: start, end: end}, nil
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
