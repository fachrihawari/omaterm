# Releasing oterm

CI runs on every push to `dev` and every pull request: `gofmt`, `go vet`,
the tests, a build, cross-compiles for macOS, Windows and Linux ARM64, and a
check that the screenshot goldens still match what the code renders.

Releases are cut from a tag:

1. Bump `version` in `mygo.config.ts` and add that version's section to
   `CHANGELOG.md`. The section becomes the release notes the update window
   shows, so write it for the user.
2. Commit and push, then tag and push the tag:

   ```sh
   git tag v0.7.0
   git push origin dev --follow-tags
   ```

3. `release.yml` checks the tag against `mygo.config.ts`, creates a draft
   release, and builds every platform into it: macOS, Windows, Linux and
   Linux ARM64. Each build is signed with the update key and carries its
   `update-<platform>.json` manifest.
4. Review the draft on the Releases page and publish it. Publishing is what
   offers the update to installed apps.

A manual run of `release.yml` is a dry run: it builds the installers and
keeps them as workflow artifacts, with no release and no updates.

## The signing key

`mygo keygen` made the pair. The public key is in `mygo.config.ts`; the
secret one is in `~/.config/mygo/update-keys/mygo-update.key` and in the
repository secret `MYGO_UPDATER_PRIVATE_KEY`. Keep it in both places: without
it, installed apps cannot be updated, and anyone who has it can ship code to
them.

## macOS and Windows signing

The workflow signs updates but not the apps themselves for other machines:
macOS builds are signed ad hoc, which Gatekeeper blocks elsewhere, and the
Windows installer is unsigned. Add the certificate secrets described in the
MyGo docs (`MACOS_CERTIFICATE`, `WINDOWS_CERTIFICATE` and the rest) and the
steps that import them to sign those too.
