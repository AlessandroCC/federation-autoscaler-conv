#!/usr/bin/env bash
#
# deploy-local.sh — run a playbook against the local KVM lab with the
# host registry wired in.
#
# Exists because of an Ansible precedence trap: `group_vars/all.yaml` is a
# FILE, and file-based group vars outrank vars written inline in an inventory.
# So putting fa_registry / fa_tag in inventory.yaml looks right and does
# nothing — the deploy silently pulls docker.io/kazem26/...:v0.1 and the agents
# then crash-loop with "flag provided but not defined: -advertised-ip", because
# that published image predates flags the current manifests pass. Passing them
# with -e is the only placement that reliably wins.
#
# Usage:
#   ./deploy-local.sh 02-deploy.yaml            # or 01-bootstrap.yaml, 03-verify.yaml
#   ./deploy-local.sh 02-deploy.yaml --check
#   FA_TAG=v0.3.1-bench ./deploy-local.sh 02-deploy.yaml

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

FA_REGISTRY="${FA_REGISTRY:-192.168.122.1:5000}"
FA_TAG="${FA_TAG:-v0.3.0-bench}"

playbook="${1:?usage: deploy-local.sh <playbook.yaml> [extra ansible args]}"
shift || true

[[ "$playbook" == playbooks/* ]] || playbook="playbooks/$playbook"

echo ">>> $playbook  (registry=$FA_REGISTRY tag=$FA_TAG)"
exec ansible-playbook -i inventory.yaml "$playbook" \
  -e "fa_registry=$FA_REGISTRY" \
  -e "fa_tag=$FA_TAG" \
  "$@"
