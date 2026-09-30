// Package policy implements the repository's salted lexical policy. Diagnostics
// expose rule identifiers, never the matched source text.
package policy

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// List is immutable after loading and safe for concurrent scans.
type List struct {
	salt   []byte
	tokens map[[32]byte]struct{}
	subs   map[int]map[[32]byte]struct{}
	widths []int
}

// Load accepts a hex-encoded random salt followed by "tok <digest>" or
// "sub <byte-length> <digest>" lines. Invalid or empty policies fail closed.
func Load(r io.Reader) (*List, error) {
	scan := bufio.NewScanner(r)
	if !scan.Scan() {
		return nil, fmt.Errorf("naming policy: missing salt")
	}
	salt, err := hex.DecodeString(scan.Text())
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return nil, fmt.Errorf("naming policy: salt must contain 16..64 random bytes in hex")
	}
	d := &List{salt: salt, tokens: make(map[[32]byte]struct{}), subs: make(map[int]map[[32]byte]struct{})}
	for line := 2; scan.Scan(); line++ {
		fields := strings.Fields(scan.Text())
		if len(fields) > 0 && !strings.HasPrefix(fields[0], "#") {
			var target map[[32]byte]struct{}
			switch {
			case len(fields) == 2 && fields[0] == "tok":
				target = d.tokens
			case len(fields) == 3 && fields[0] == "sub":
				n, parseErr := strconv.Atoi(fields[1])
				if parseErr != nil || n < 5 || n > 256 {
					return nil, fmt.Errorf("naming policy: invalid substring length at line %d", line)
				}
				if d.subs[n] == nil {
					d.subs[n] = make(map[[32]byte]struct{})
					d.widths = append(d.widths, n)
				}
				target = d.subs[n]
			default:
				return nil, fmt.Errorf("naming policy: invalid rule at line %d", line)
			}
			encoded := fields[len(fields)-1]
			raw, decodeErr := hex.DecodeString(encoded)
			if decodeErr != nil || len(raw) != sha256.Size || encoded != strings.ToLower(encoded) {
				return nil, fmt.Errorf("naming policy: invalid digest at line %d", line)
			}
			var digest [32]byte
			copy(digest[:], raw)
			if _, exists := target[digest]; exists {
				return nil, fmt.Errorf("naming policy: duplicate rule at line %d", line)
			}
			target[digest] = struct{}{}
		}
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("naming policy: read failed: %w", err)
	}
	if len(d.tokens)+len(d.subs) == 0 {
		return nil, fmt.Errorf("naming policy: no rules")
	}
	sort.Ints(d.widths)
	return d, nil
}

func (d *List) sum(term string) [32]byte {
	h := sha256.New()
	_, _ = h.Write(d.salt)
	_, _ = io.WriteString(h, term)
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

// Rule hashes one name read by the maintenance command. It returns only the
// rule, so the plaintext does not enter the checked-in policy or CI logs.
func (d *List) Rule(kind, name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if len(name) == 0 || len(name) > 256 {
		return "", fmt.Errorf("name must contain 1..256 bytes")
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return "", fmt.Errorf("name must contain only letters and digits")
		}
	}
	sum := d.sum(name)
	switch kind {
	case "tok":
		return fmt.Sprintf("tok %x", sum), nil
	case "sub":
		if len(name) < 5 {
			return "", fmt.Errorf("substring names must contain at least five bytes")
		}
		return fmt.Sprintf("sub %d %x", len(name), sum), nil
	default:
		return "", fmt.Errorf("rule kind must be tok or sub")
	}
}

// Hit reports the first matching rule. Callers may expose term only in an
// explicit local explain command, never in normal gate diagnostics.
func (d *List) Hit(line string) (rule, term string) {
	for _, candidate := range terms(line) {
		sum := d.sum(candidate)
		if _, ok := d.tokens[sum]; ok {
			return fmt.Sprintf("tok-%x", sum[:4]), candidate
		}
		for _, width := range d.widths {
			for start := 0; start+width <= len(candidate); start++ {
				part := candidate[start : start+width]
				sum := d.sum(part)
				if _, ok := d.subs[width][sum]; ok {
					return fmt.Sprintf("sub-%x", sum[:4]), part
				}
			}
		}
	}
	return "", ""
}

// terms covers case changes, acronym boundaries, digit boundaries and adjacent
// words separated by hyphens, underscores or whitespace. Syntax delimiters do
// not join unrelated names such as a function name and its argument.
func terms(line string) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	words := strings.FieldsFunc(line, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	end := 0
	for i, word := range words {
		add(strings.ToLower(word))
		start := end + strings.Index(line[end:], word)
		if i > 0 && joinsName(line[end:start]) {
			add(strings.ToLower(words[i-1] + word))
		}
		end = start + len(word)
		parts := splitCamel(word)
		for j, part := range parts {
			add(part)
			if j+1 < len(parts) {
				add(part + parts[j+1])
			}
		}
	}
	return out
}

func joinsName(separator string) bool {
	for _, r := range separator {
		if r != '-' && r != '_' && !unicode.IsSpace(r) {
			return false
		}
	}
	return separator != ""
}

func splitCamel(word string) []string {
	runes := []rune(word)
	start := 0
	var out []string
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		boundary := unicode.IsDigit(prev) != unicode.IsDigit(cur)
		boundary = boundary || (unicode.IsLower(prev) && unicode.IsUpper(cur))
		boundary = boundary || (unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(runes) && unicode.IsLower(runes[i+1]))
		if boundary {
			out = append(out, strings.ToLower(string(runes[start:i])))
			start = i
		}
	}
	return append(out, strings.ToLower(string(runes[start:])))
}

// Excluded is the only content exemption. The repository-owned test driver
// manifest remains subject to the same policy as source and test fixtures.
func Excluded(path string) bool {
	return strings.HasPrefix(path, "drivers/") && path != "drivers/fakecli.yaml"
}
