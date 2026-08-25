#!/bin/bash
# Cuts a new GitHub release: builds all platform binaries via build.sh,
# tags the commit, and uploads the binaries as release assets so
# `dictate -update` can find them.
#
# Requires the GitHub CLI (brew install gh) and a token scoped to this
# repo. Put it in a git-ignored .env file here (GH_TOKEN=... -- already
# covered by .gitignore's `.*` rule) or export GH_TOKEN yourself before
# running this script. No `gh auth login` needed: gh reads GH_TOKEN
# directly, and the git push below is authenticated with the same token
# via a one-off header instead of being written into .git/config.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

if [ -f .env ]; then
  set -a
  source .env
  set +a
fi

if [ $# -ne 1 ]; then
  echo "usage: $0 <version>   (e.g. $0 1.1.0 -- no leading 'v')" >&2
  exit 1
fi
VERSION="$1"

if ! command -v gh >/dev/null 2>&1; then
  echo "gh (GitHub CLI) is required: brew install gh" >&2
  exit 1
fi

if [ -z "${GH_TOKEN:-}" ]; then
  echo "GH_TOKEN is not set -- put it in a local .env file (GH_TOKEN=...) or export it before running this script" >&2
  exit 1
fi

if [ -n "$(git status --porcelain)" ]; then
  echo "working tree has uncommitted changes -- commit or stash first" >&2
  exit 1
fi

./build.sh "$VERSION"

git tag "$VERSION"
basic_credential="$(printf '%s' "x-access-token:${GH_TOKEN}" | base64 | tr -d '\n')"
git -c http.extraheader="AUTHORIZATION: basic ${basic_credential}" push origin "$VERSION"

gh release create "$VERSION" dist/dictate-* \
  --title "v$VERSION" \
  --generate-notes

echo
echo "released $VERSION -- existing installs can now run: dictate -update"
