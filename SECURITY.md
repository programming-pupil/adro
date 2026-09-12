# Security policy

Please do not open a public issue for a suspected vulnerability. Use [GitHub's private vulnerability reporting](https://github.com/programming-pupil/adro/security/advisories/new) for this repository, or contact the maintainers through the repository's private security channel, with the affected version, a minimal reproduction and impact. Do not include credentials or customer data.

Security reports are acknowledged privately, triaged by severity, and fixed through a reviewed change. Reporters should allow maintainers reasonable time to reproduce and release a fix before public disclosure.

The control-plane threat model and required production controls are documented in [docs/architecture/production-deployment.md](docs/architecture/production-deployment.md). The local profile is intentionally not a production identity, runner or secret-management boundary.
