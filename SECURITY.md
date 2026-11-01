# Security reports

Please use the repository's [private vulnerability reporting](https://github.com/thomasmikava/WebDeviceCable/security/advisories/new) for security reports. Do not post exploit details publicly. No dedicated response-time SLA is promised for this community test release.

Include the WDC version, OS/architecture, affected boundary and a minimal reproduction. Do not send native credentials, invitations, control links, pairing keys, private prompts or your state directory. Supported boundaries, trust assumptions and known limitations are in [security notes](docs/security.md) and [release gates](docs/operations.md).

Only the latest community release and current source are maintained. Official update trust and maintainer key handling are described in [release steps](docs/releases.md). This project has not yet received an independent security audit. Community downloads use authenticated checksum manifests and manual updates, with ad-hoc macOS signatures and unsigned Windows executables. They are an early-access testing channel; real Windows/Linux native-provider validation remains pending.

The optional self-hosted relay is a trusted project server; see [its transport and retention boundaries](docs/relay.md). It never tunnels desktop administration or receives provider credentials.
