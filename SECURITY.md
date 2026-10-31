# Security reports

Please use the repository's [private vulnerability reporting](https://github.com/thomasmikava/WebDeviceCable/security/advisories/new) when enabled. If it is unavailable, ask the maintainer for a private contact without posting exploit details publicly. No dedicated response-time SLA is promised for this preview.

Include the WDC version, OS/architecture, affected boundary and a minimal reproduction. Do not send native credentials, invitations, control links, pairing keys, private prompts or your state directory. Supported boundaries, trust assumptions and known limitations are in [security notes](docs/security.md) and [release gates](docs/operations.md).

Only the latest release and preview are maintained. Maintainers should enable GitHub private vulnerability reporting before making the repository public. Official update trust and maintainer key handling are described in [release steps](docs/releases.md). This project has not yet received an independent security audit; unsigned preview builds are not production releases.

The optional self-hosted relay is a trusted project server; see [its transport and retention boundaries](docs/relay.md). It never tunnels desktop administration or receives provider credentials.
