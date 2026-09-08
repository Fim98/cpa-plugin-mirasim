# Plugin store publishing

The publication contract follows the [official CLIProxyAPI Plugins Store requirements](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store#release-requirements). The store maintains metadata; the plugin repository hosts the release binaries and checksums.

The planned public repository is `https://github.com/KIDA-MNESIA/cpa-plugin-mirasim`, with author `KIDA-MNESIA`. The root [registry.json](../registry.json) is ready for that address. Publishing this file does not register the plugin in the official store, and the planned repository must be created and populated before its URLs work.

## Release contract

- Registry schema: `1`; plugin ID: `mirasim`; no pinned `version`.
- Tag: `v` followed by a dotted numeric version, for example `v0.7.1`.
- Each asset: `mirasim_<version>_<goos>_<goarch>.zip`.
- ZIP contents: exactly one nonempty regular file at the root, named `mirasim.so`, `mirasim.dylib`, or `mirasim.dll` for its target OS.
- Platforms: Linux, macOS, and Windows on amd64 and arm64; FreeBSD on amd64.
- `checksums.txt`: SHA-256 of every ZIP in standard sha256sum format.

The workflow rejects unsupported tag formats before the build jobs start. After all builds succeed, `scripts/plugin_store.py verify-release` checks the complete platform set, ZIP layout, and per-archive checksum sidecars, then writes the combined `checksums.txt`. No release upload runs if validation fails. Keep the workflow build matrix and `PLATFORMS` in that script aligned when adding or removing platforms.

ZIP validation checks packaging, not the binary's ABI or runtime behavior. The test fixtures likewise do not establish that a real plugin can load.

## Publish and test

1. Push the repository, including `.github`, `scripts`, and `registry.json`, to the planned GitHub address. Preserve existing local remotes as needed.
2. Push the intended numeric version tag. The workflow injects the tag version without `v` into `main.pluginVersion` and publishes all seven ZIPs plus `checksums.txt`.
3. Confirm the run succeeded, the release is published rather than a draft or prerelease, and GitHub's **Latest** release points to the version intended for store users. Rerunning an old tag should not be used to promote it as Latest.
4. Verify the release assets and test the custom source below with a compatible CLIProxyAPI host before requesting official listing.

After `registry.json` is available on GitHub's `main` branch, add it to a test CPA configuration:

```yaml
plugins:
  enabled: true
  dir: plugins
  store-sources:
    - https://raw.githubusercontent.com/KIDA-MNESIA/cpa-plugin-mirasim/main/registry.json
```

The official source remains built in; this adds the custom source. Refresh the store, install Mirasim, enable it, and reload or restart the host as required. Confirm the host reports plugin ID `mirasim` and the expected version. Complete OAuth and validate an actual Claude Code or Codex client request, correlating the result with CPA logs as described in the [README](../README.md#functional-validation).

The store installs only the native plugin library. The optional OAuth loopback bridge and the custom Management Center quota UI still follow their separate build/setup instructions in the README; they are not installed by a plugin ZIP.

## Prepare the official-store submission

Use Python 3.11 or later. Replace the example tag with the release that was actually published:

```powershell
python scripts/plugin_store.py prepare-submission `
  --repository https://github.com/KIDA-MNESIA/cpa-plugin-mirasim `
  --author KIDA-MNESIA `
  --tag v0.7.1
```

This writes `dist/store/registry.json` and `dist/store/store-pr.md`. The draft includes the repository, release, every asset link, and the capability description. It does not query GitHub or claim that those links exist. Verify them and replace its checklist with actual installation and client-test results before submitting.

If the repository or author changes, regenerate the files and copy the generated registry to the repository root before publishing the custom source. Do not add a fixed version to the registry for routine updates.

Fork [CLIProxyAPI-Plugins-Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store), check that `mirasim` is not already registered, and append the generated `plugins[0]` object to the official `registry.json`. Preserve all existing entries and submit only that registry change with the verified PR description. Official listing depends on the store maintainers merging the PR. Future updates normally require only a new latest release in this plugin's repository.

## Local checks

```powershell
python -m unittest discover -s scripts -p test_plugin_store.py -v
python scripts/plugin_store.py validate-tag --tag v0.7.1
python scripts/plugin_store.py verify-release --tag v0.7.1 --directory dist/release
```

For the last command, collect the workflow's seven ZIPs and their `.zip.sha256` sidecars in `dist/release`. A missing platform, extra archive, wrong tag, mismatched hash, unsafe layout, empty library, or multiple ZIP entries fails validation.
