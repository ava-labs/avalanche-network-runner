// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package utils

import (
	"bufio"
	"fmt"
	"strings"
)

// Update the JSON body if the matching key is found
// and replace the value.
// e.g., "whitelisted-subnets" is the key and value is "a,b,c".
func UpdateJSONKey(jsonBody string, k string, v string) (string, error) {
	k = fmt.Sprintf(`"%s":`, k)
	lines := make([]string, 0)
	scanner := bufio.NewScanner(strings.NewReader(jsonBody))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, k) {
			lines = append(lines, line)
			continue
		}

		idx := strings.Index(line, k)
		if idx == -1 {
			// should never happen...
			return "", fmt.Errorf("line %q is missing the key %q", line, k)
		}
		line = line[:idx]
		line += fmt.Sprintf(`%s"%s"`, k, v)
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), scanner.Err()
}
