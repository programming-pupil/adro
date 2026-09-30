# Repository name policy

`go run ./tools/namingdeny check` scans tracked paths, staged blobs and working
files, including dependency checksums, test data, documentation and licenses.
The only excluded content is deployment-owned data in `drivers/`;
`drivers/fakecli.yaml` is checked. Git objects are streamed through one bounded
batch reader. A missing file, unresolved merge, oversized file or failed Git
process fails the check. Symlinks are inspected as link text, never followed
outside the checkout. Binary blobs containing NUL are excluded from text scans.

The policy lives in `testkit/golden/naming/denylist.sha256`. Its first line is a
hex-encoded random salt (decoded to bytes before hashing). Other lines use
`tok <sha256>` for whole tokens or `sub <byte-length> <sha256>` for substrings of
at least five bytes. Digests cover the salt followed by the lowercase name.
Tokens cover case changes, acronym boundaries, digits and adjacent components
separated by hyphens, underscores or whitespace. Syntax delimiters do not join
unrelated function names and arguments. Malformed or empty policies fail.

To produce a proposed rule, run `go run ./tools/namingdeny add -kind sub` and
enter one normalized name per line on standard input. Use `-kind tok` for short
names. The command prints only hashed rules; review and add those lines to the
policy. Only maintainers should approve policy changes. Plaintext maintenance
lists must remain outside version control.

Normal diagnostics report paths, line numbers and hashed rule identifiers.
`go run ./tools/namingdeny explain path/to/file:42` explicitly reveals the
matching term from that local line. Line 0 explains a path match.

The initial policy incorporates both built-in rules from the frozen baseline,
any supplied private lexical policy, and the four required naming categories.
Public checks need no private file or credentials.
