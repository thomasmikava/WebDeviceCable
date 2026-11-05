# npm packages and publishing

WDC distributes two independent, MIT-licensed packages. Both use the repository's `package.json` version, include declarations for ESM and CommonJS, have no runtime dependencies and run no install scripts.

| Package | Consumer | Entry points |
|---|---|---|
| `@webdevicecable/sdk` | Frontend connecting directly to installed WDC | Native browser ESM, ESM/CommonJS frontend tooling |
| `@webdevicecable/relay` | Node.js 22+ backend connecting to a deployed relay | ESM and CommonJS, including `RelayAdminClient` |

The browser package contains no relay code. The backend package includes its own required modules and does not depend on the browser package or another checkout. Docker and desktop applications are separate artifacts. Building an npm archive never publishes it.

## Prepare and inspect locally

```sh
npm ci --ignore-scripts
npm run check:sdk-drift
npm run test:sdk
npm run test:package
```

`test:package` builds and installs the actual tarballs into a disposable consumer outside the source checkout, checks the complete file lists, imports both module formats, tests NodeNext and bundler TypeScript resolution, verifies backend declarations without DOM libraries, loads browser modules through HTTP without Node globals, exercises account-scoped browser key storage and performs offline publish dry runs. It also checks that the relay package loads without the browser package installed. It uses only fixture requests and temporary state; no inference or registry writes occur.

The relay ESM facade is generated from the TypeScript compiler's actual value exports. Packaging fails if either format omits an export or the relay formats have different runtime identities. The installed-tarball test independently compares every value export for both packages against the compiler output, rather than maintaining a list of selected APIs. A negative control recreates the broken `0.1.1` relay ESM facade and requires the gate to reject it. These checks run before publication in the GitHub publishing workflow. Direct `npm publish` can bypass repository checks; use only an archive that passed this gate and verify its integrity against the public registry after publication.

For version `0.2.1`, review:

- `dist/webdevicecable-sdk-0.2.1.tgz` and `dist/sdk/`.
- `dist/webdevicecable-relay-0.2.1.tgz` and `dist/relay-client/`.

Only runtime JavaScript, declarations, package metadata, license, README and the relevant integration guide enter each archive. Packaged guides link back to the source documentation. The root package stays private to prevent accidentally publishing the whole repository.

For a new version, run `npm version X.Y.Z --no-git-tag-version`, review the root manifest and lockfile changes, and commit the release source before invoking CI. Do not edit generated manifests under `dist`. Use a new version for changed package bytes; npm versions cannot be replaced. Prereleases must use the `next` distribution tag.

Tag the reviewed source commit `npm-vX.Y.Z` and push that tag with the source before publishing. Packaged guides link to that fixed tag; the publishing workflow checks that it matches its source commit. npm tags are separate from desktop download release tags (`vX.Y.Z`).

## First publication and ownership

Before publishing, create or obtain publishing access to the `@webdevicecable` npm scope. A local package name or a registry 404 does not reserve that scope. Public scoped packages require explicit public access. See [npm's scoped package instructions](https://docs.npmjs.com/creating-and-publishing-scoped-public-packages/).

Trusted publishers are configured in each existing package's npm settings. If these packages do not exist yet, a maintainer with scope access must create them through an authenticated first publication. After reviewing the tested tarballs on that maintainer's machine, the first-release commands are:

```sh
npm login --auth-type=web
npm publish ./dist/webdevicecable-sdk-0.2.1.tgz --access public --ignore-scripts
npm publish ./dist/webdevicecable-relay-0.2.1.tgz --access public --ignore-scripts
```

These commands publish real packages. They are not part of the preparation or test scripts. Authenticate with the publishing account's required 2FA. A local first publication does not carry GitHub Actions build provenance; subsequent releases through the workflow below do. Do not publish placeholder packages or put an npm token in source, frontend configuration or a Docker image.

## Configure trusted publishing

For **each** package, add this GitHub Actions trusted publisher in npm's package settings:

| Field | Value |
|---|---|
| Organization or user | `thomasmikava` |
| Repository | `WebDeviceCable` |
| Workflow filename | `publish-npm.yml` |
| Environment | `release` |
| Allowed action | Direct `npm publish` |

Create the `release` GitHub Environment, restrict its deployment branch to the repository's default branch and configure its maintainers/reviewers. The repository must be public for npm provenance. Do not add `NPM_TOKEN` or `NODE_AUTH_TOKEN`: the publish jobs use short-lived OIDC credentials. Current npm requires Node >=22.14.0 and npm >=11.5.1 for trusted publishing; the workflow selects Node 24 and checks npm tooling. npm currently expires a new, unused trusted-publisher configuration after two days, so configure it near the next actual release. Follow the [official trusted publishing instructions](https://docs.npmjs.com/trusted-publishers/) if these requirements change.

Forks must change the package names, repository metadata, workflow repository guard and trusted-publisher identity before publishing their own packages.

## Release workflow

Run **Publish npm packages** on the default branch, supplying the exact root version and `latest` or `next` tag. `dry_run` defaults to true. A dry run validates source/build drift, runs the SDK and installed-package checks, and uploads both reviewed tarballs as the `npm-packages` workflow artifact without publishing.

For a subsequent real release, select `dry_run: false`. The same preparation runs, then two jobs in the protected `release` environment publish those exact verified tarballs with provenance. They do not rebuild the artifacts or run package lifecycle scripts. The npm account/scope configuration and actual publication remain separate from local preparation.

Publishing the two packages is not atomic. If one succeeds and the other fails, rerun only the failed publish job against that run's retained artifact. Do not rerun a successful publication or change the bytes under the existing version. If the artifact has expired, inspect the registry and prepare a new version rather than guessing what was published.

## Browser distribution by npm, URL or static files

Once the version exists in npm, a frontend such as Rorido installs it normally:

```sh
npm install --save-exact @webdevicecable/sdk@0.2.1
```

```js
import { BridgeClient, ConnectionStore } from '@webdevicecable/sdk';
```

Plain HTML applications can import the identical native ESM files from a version-pinned npm CDN:

```html
<script type="module">
  import { BridgeClient, ConnectionStore }
    from 'https://unpkg.com/@webdevicecable/sdk@0.2.1/index.js';
</script>
```

jsDelivr also serves `https://cdn.jsdelivr.net/npm/@webdevicecable/sdk@0.2.1/index.js`. These URLs become usable only after publication; local tests validate the equivalent HTTP module layout, not a nonexistent CDN release. [UNPKG](https://unpkg.com/) and [jsDelivr](https://www.jsdelivr.com/documentation) serve npm files, so WDC needs no separate SDK hosting service. Pin a complete version rather than `latest`.

For self-hosting, copy `index.js`, `secure.js` and `storage.js` from the same archive into one static directory and import its `index.js`. Load it as a module. No global script build is provided. Browser local-network policy still applies to connections to installed WDC; changing where SDK code is downloaded does not change that connection.

Rorido and Kings should pin the coordinated 0.2.1 registry packages when adopting this release. Before publication, validate against the prepared local archives without committing filesystem dependencies. Make compatible desktop and relay versions available before deploying the application upgrades. Publication alone does not change application deployments or installed dependencies.
