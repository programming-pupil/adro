// Command namingdeny checks repository names and maintains the hashed policy.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/adro-project/adro/tools/namingdeny/policy"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: namingdeny check|add|explain [options]")
	}
	command := args[0]
	flags := new(flag.FlagSet)
	flags.SetOutput(io.Discard)
	root := flags.String("root", ".", "repository root")
	list := flags.String("list", "testkit/golden/naming/denylist.sha256", "policy path relative to root")
	kind := flags.String("kind", "tok", "rule kind for add: tok or sub")
	if err := flags.Parse(args[1:]); err != nil {
		return fmt.Errorf("namingdeny: invalid arguments")
	}
	f, err := os.Open(filepath.Join(*root, *list))
	if err != nil {
		return fmt.Errorf("namingdeny: open policy: %w", err)
	}
	d, loadErr := policy.Load(f)
	closeErr := f.Close()
	if loadErr != nil {
		return loadErr
	}
	if closeErr != nil {
		return closeErr
	}
	switch command {
	case "check":
		if flags.NArg() != 0 {
			return fmt.Errorf("check accepts no positional arguments")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		violations, err := policy.Scan(ctx, *root, d)
		if err != nil {
			return err
		}
		for _, violation := range violations {
			if _, err := fmt.Fprintln(out, violation); err != nil {
				return err
			}
		}
		if len(violations) > 0 {
			return fmt.Errorf("naming: %d violations", len(violations))
		}
		_, err = fmt.Fprintln(out, "naming: passed (index and worktree)")
		return err
	case "add":
		if flags.NArg() != 0 {
			return fmt.Errorf("add reads names from standard input only")
		}
		scan := bufio.NewScanner(in)
		count := 0
		for scan.Scan() {
			row, err := d.Rule(*kind, scan.Text())
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(out, row); err != nil {
				return err
			}
			count++
		}
		if err := scan.Err(); err != nil {
			return fmt.Errorf("namingdeny: read names: %w", err)
		}
		if count == 0 {
			return fmt.Errorf("namingdeny: no names on standard input")
		}
		return nil
	case "explain":
		if flags.NArg() != 1 {
			return fmt.Errorf("explain requires a file:line location")
		}
		return explain(*root, flags.Arg(0), d, out)
	default:
		return fmt.Errorf("usage: namingdeny check|add|explain [options]")
	}
}

func explain(root, location string, d *policy.List, out io.Writer) error {
	sep := strings.LastIndexByte(location, ':')
	if sep < 0 {
		return fmt.Errorf("explain requires a file:line location")
	}
	line, err := strconv.Atoi(location[sep+1:])
	if err != nil || line < 0 {
		return fmt.Errorf("explain requires a nonnegative line number")
	}
	path := location[:sep]
	if !filepath.IsLocal(path) {
		return fmt.Errorf("explain requires a repository-relative file")
	}
	if line == 0 {
		return explainText(d, path, out)
	}
	f, err := os.Open(filepath.Join(root, path))
	if err != nil {
		return err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 16<<20)
	for n := 1; scan.Scan(); n++ {
		if n == line {
			return explainText(d, scan.Text(), out)
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	return fmt.Errorf("line is outside file")
}

func explainText(d *policy.List, text string, out io.Writer) error {
	rule, term := d.Hit(text)
	if rule == "" {
		return fmt.Errorf("no policy match at this location")
	}
	_, err := fmt.Fprintf(out, "%s %q\n", rule, term)
	return err
}
