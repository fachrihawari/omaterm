# Releasing omaterm

CI runs on every push to `dev` and every pull request: `gofmt`, `go vet`,
the tests, a build, and cross-compiles for macOS, Windows and Linux ARM64. A
second job renders every screenshot and keeps them as an artifact; it does
not compare bytes yet, because the goldens depend on the system's fonts
(Inter and JetBrains Mono, else whatever the runner has).

Releases are cut from a tag:

1. Bump `version` in `mygo.json` and add that version's section to
   `CHANGELOG.md`. The section becomes the release notes the update window
   shows, so write it for the user.
2. Commit and push, then tag and push the tag:

   ```sh
   git tag v0.7.0
   git push origin dev --follow-tags
   ```

3. `release.yml` checks the tag against `mygo.json`, creates a draft
   release, and builds every platform into it: macOS, Windows, Linux and
   Linux ARM64. Each build is signed with the update key and carries its
   `update-<platform>.json` manifest.
4. Review the draft on the Releases page and publish it. Publishing is what
   offers the update to installed apps.

A manual run of `release.yml` is a dry run: it builds the installers and
keeps them as workflow artifacts, with no release and no updates.

## The signing key

`mygo keygen` made the pair. The public key is in `mygo.json`; the secret one
is in `~/.config/mygo/update-keys/mygo-update.key` and in the repository
secret `MYGO_UPDATER_PRIVATE_KEY`.

It is not a build credential you can rotate freely. Every published app has
the public key built in and accepts only updates signed with its pair, so:

- **Without it**, no release can reach installed users again. They would all
  have to install a version carrying a new public key by hand.
- **With it**, anyone can ship code to every user as a valid update.

Keep a copy outside this machine as well: a password manager entry holding
the 88-character key, or an encrypted note. The file on disk and the GitHub
secret can both be lost in one accident — a dead disk, a lost laptop, a
locked account — and neither is a backup of the other. Store the account's
two-factor recovery codes somewhere else again.

The key never goes in the repository: `.gitignore` covers `*.key`,
`mygo-update.key` and `update-keys/`, and no commit has ever contained it.
Nothing else needs it. `go build`, `go test` and `mygo dev` work without it;
only `mygo build` signs an update, and CI reads it from the secret.

If it is lost, there is no recovery. `mygo keygen -force` makes a new pair,
`mygo.json` takes the new public key, and the next release ships it — but
installed apps treat it as a stranger and need one manual install.

## macOS and Windows signing

The workflow signs updates but not the apps themselves for other machines:
macOS builds are signed ad hoc, which Gatekeeper blocks elsewhere, and the
Windows installer is unsigned. Add the certificate secrets described in the
MyGo docs (`MACOS_CERTIFICATE`, `WINDOWS_CERTIFICATE` and the rest) and the
steps that import them to sign those too.
