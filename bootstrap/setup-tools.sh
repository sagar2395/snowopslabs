#!/usr/bin/env bash
set -euo pipefail

# Detect host OS and architecture once; all download URLs use these.
OS="$(uname -s | tr '[:upper:]' '[:lower:]')" # linux | darwin
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64 | amd64) ARCH=amd64 ;;
  arm64 | aarch64) ARCH=arm64 ;;
  *)
    echo "ERROR: unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSION_FILE="${SCRIPT_DIR}/../versions.env"

if [ ! -f "$VERSION_FILE" ]; then
  echo "Error: versions.env not found at $VERSION_FILE" >&2
  exit 1
fi

# shellcheck source=/dev/null
source "$VERSION_FILE"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

INSTALL_DIR="/usr/local/bin"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# Create /usr/local/bin if absent (default on Apple Silicon Macs).
ensure_install_dir() {
  if [ ! -d "$INSTALL_DIR" ]; then
    echo "Creating $INSTALL_DIR..."
    sudo mkdir -p "$INSTALL_DIR"
  fi
}

# version_ge <have> <want> — succeeds when <have> >= <want> as a semver.
#
# The pinned versions in versions.env are MINIMUMS, not exact matches. A user
# who brought their own newer helm (via brew, apt, whatever) should not have
# their tool replaced by an older one — and on Apple Silicon that "replacement"
# would fail anyway because brew's /opt/homebrew/bin shadows /usr/local/bin.
#
# A leading 'v' is stripped from both sides, because tools disagree about it
# ('helm version --short' emits it, 'kubectl' does not) and sort -V would
# otherwise treat "v3.14.0" as textually greater than "4.2.3" — the leading 'v'
# sorts after any digit.
#
# Uses sort -V, which is portable across macOS and modern Linux.
version_ge() {
  [ -z "$1" ] && return 1
  [ -z "$2" ] && return 0
  local have="${1#v}" want="${2#v}"
  # If sorting the two versions numerically puts <want> first, then <have>
  # >= <want>. `printf '%s\n%s\n' | sort -V | head -1` returns the smaller.
  local smallest
  smallest="$(printf '%s\n%s\n' "$have" "$want" | sort -V | head -n 1)"
  [ "$smallest" = "$want" ]
}

# Block until Docker daemon responds or timeout.
_wait_for_docker() {
  local retries=0
  echo "Waiting for Docker daemon..."
  until docker info &>/dev/null; do
    retries=$((retries + 1))
    if [ "$retries" -ge 60 ]; then
      echo -e "${RED}ERROR: Docker daemon not reachable after 60 s.${NC}" >&2
      echo "Check logs with: colima status   (macOS) or   sudo journalctl -u docker   (Linux)" >&2
      exit 1
    fi
    sleep 1
  done
}

# ---------------------------------------------------------------------------
# Tool installers
# ---------------------------------------------------------------------------

install_kubectl() {
  echo -e "${YELLOW}Checking kubectl (minimum v${KUBECTL_VERSION})...${NC}"

  if command -v kubectl &>/dev/null; then
    current_version=$(kubectl version --client 2>/dev/null |
      grep "Client Version:" | awk '{print $NF}' | sed 's/v//')
    if version_ge "$current_version" "${KUBECTL_VERSION}"; then
      echo -e "${GREEN}kubectl v${current_version} already installed (meets minimum v${KUBECTL_VERSION})${NC}"
      return 0
    fi
    echo -e "${YELLOW}kubectl v${current_version} is below the minimum v${KUBECTL_VERSION} — installing a newer one${NC}"
  fi

  ensure_install_dir
  echo "Downloading kubectl v${KUBECTL_VERSION} for ${OS}/${ARCH}..."
  curl -fsSLo /tmp/kubectl \
    "https://dl.k8s.io/release/v${KUBECTL_VERSION}/bin/${OS}/${ARCH}/kubectl"
  chmod +x /tmp/kubectl
  sudo mv /tmp/kubectl "${INSTALL_DIR}/kubectl"

  installed=$(kubectl version --client 2>/dev/null |
    grep "Client Version:" | awk '{print $NF}' | sed 's/v//')
  if version_ge "$installed" "${KUBECTL_VERSION}"; then
    echo -e "${GREEN}kubectl v${installed} installed and verified${NC}"
  else
    echo -e "${RED}ERROR: kubectl v${installed} still below the minimum v${KUBECTL_VERSION} — is another kubectl earlier on PATH?${NC}" >&2
    exit 1
  fi
}

install_k3d() {
  echo -e "${YELLOW}Checking k3d (minimum v${K3D_VERSION})...${NC}"

  if command -v k3d &>/dev/null; then
    current_version=$(k3d version 2>/dev/null |
      grep 'k3d version' |
      sed 's/.*k3d version v\([^ -]*\).*/\1/')
    if version_ge "$current_version" "${K3D_VERSION}"; then
      echo -e "${GREEN}k3d v${current_version} already installed (meets minimum v${K3D_VERSION})${NC}"
      return 0
    fi
    echo -e "${YELLOW}k3d v${current_version} is below the minimum v${K3D_VERSION} — installing a newer one${NC}"
  fi

  ensure_install_dir
  echo "Downloading k3d v${K3D_VERSION} for ${OS}-${ARCH}..."
  curl -fsSLo /tmp/k3d \
    "https://github.com/k3d-io/k3d/releases/download/v${K3D_VERSION}/k3d-${OS}-${ARCH}"
  chmod +x /tmp/k3d
  sudo mv /tmp/k3d "${INSTALL_DIR}/k3d"

  installed=$(k3d version 2>/dev/null |
    grep 'k3d version' |
    sed 's/.*k3d version v\([^ -]*\).*/\1/')
  if version_ge "$installed" "${K3D_VERSION}"; then
    echo -e "${GREEN}k3d v${installed} installed and verified${NC}"
  else
    echo -e "${RED}ERROR: k3d v${installed} still below the minimum v${K3D_VERSION} — is another k3d earlier on PATH?${NC}" >&2
    exit 1
  fi
}

install_helm() {
  echo -e "${YELLOW}Checking Helm (minimum v${HELM_VERSION})...${NC}"

  if command -v helm &>/dev/null; then
    current_version=$(helm version --short 2>/dev/null |
      sed 's/v\([^+]*\).*/\1/')
    if version_ge "$current_version" "${HELM_VERSION}"; then
      echo -e "${GREEN}Helm v${current_version} already installed (meets minimum v${HELM_VERSION})${NC}"
      return 0
    fi
    echo -e "${YELLOW}Helm v${current_version} is below the minimum v${HELM_VERSION} — installing a newer one${NC}"
  fi

  ensure_install_dir
  echo "Downloading Helm v${HELM_VERSION} for ${OS}-${ARCH}..."
  local helm_tmp
  helm_tmp="$(mktemp -d)"
  curl -fsSLo "${helm_tmp}/helm.tar.gz" \
    "https://get.helm.sh/helm-v${HELM_VERSION}-${OS}-${ARCH}.tar.gz"
  tar -xzf "${helm_tmp}/helm.tar.gz" -C "${helm_tmp}"
  sudo mv "${helm_tmp}/${OS}-${ARCH}/helm" "${INSTALL_DIR}/helm"
  rm -rf "${helm_tmp}"

  installed=$(helm version --short 2>/dev/null | sed 's/v\([^+]*\).*/\1/')
  if version_ge "$installed" "${HELM_VERSION}"; then
    echo -e "${GREEN}Helm v${installed} installed and verified${NC}"
  else
    # This is the shadowed-PATH case: brew's /opt/homebrew/bin/helm wins
    # over the /usr/local/bin/helm we just wrote. Say so, don't just fail.
    actual_path="$(command -v helm)"
    echo -e "${RED}ERROR: helm on PATH is v${installed}, below the minimum v${HELM_VERSION}.${NC}" >&2
    echo -e "${RED}  Found: ${actual_path}${NC}" >&2
    echo -e "${RED}  Just installed: ${INSTALL_DIR}/helm${NC}" >&2
    echo -e "${YELLOW}  A newer helm at ${actual_path} is shadowing it. Either:${NC}" >&2
    echo -e "${YELLOW}    - upgrade the one on PATH: 'brew upgrade helm' (macOS) / your package manager${NC}" >&2
    echo -e "${YELLOW}    - or reorder PATH so ${INSTALL_DIR} comes first${NC}" >&2
    exit 1
  fi
}

# ---------------------------------------------------------------------------
# Container runtime — Colima (macOS) or Docker Engine (Linux)
# ---------------------------------------------------------------------------

# macOS: ensure Homebrew is present.
_install_homebrew() {
  if command -v brew &>/dev/null; then
    echo -e "${GREEN}Homebrew already installed${NC}"
    return 0
  fi
  echo "Installing Homebrew (this may take a few minutes)..."
  NONINTERACTIVE=1 /bin/bash -c \
    "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  # Activate brew in the current shell — path differs between Apple Silicon and Intel.
  if [ -x /opt/homebrew/bin/brew ]; then
    eval "$(/opt/homebrew/bin/brew shellenv)"
  elif [ -x /usr/local/bin/brew ]; then
    eval "$(/usr/local/bin/brew shellenv)"
  fi
  echo -e "${GREEN}Homebrew installed${NC}"
}

# macOS: install Colima + Docker CLI, then start the VM.
install_colima() {
  echo -e "${YELLOW}Setting up Colima (macOS container runtime)...${NC}"

  if ! command -v colima &>/dev/null; then
    _install_homebrew
    echo "Installing colima and docker CLI via Homebrew..."
    brew install colima docker
  else
    echo -e "${GREEN}Colima already installed${NC}"
  fi

  if colima status &>/dev/null; then
    echo -e "${GREEN}Colima already running${NC}"
    return 0
  fi

  echo "Starting Colima (first run may download a VM image — ~1 min)..."
  colima start
  _wait_for_docker
  echo -e "${GREEN}Colima started — Docker daemon ready${NC}"
}

# Linux: detect distro ID from /etc/os-release.
_linux_distro() {
  if [ -f /etc/os-release ]; then
    # shellcheck source=/dev/null
    . /etc/os-release
    echo "${ID:-unknown}"
  else
    echo "unknown"
  fi
}

# Linux Debian/Ubuntu family — Docker's official apt repo.
_docker_apt() {
  local distro="$1"
  echo "Installing Docker Engine via apt (${distro})..."
  sudo apt-get update -qq
  sudo apt-get install -y -qq ca-certificates curl gnupg lsb-release
  sudo install -m 0755 -d /etc/apt/keyrings
  curl -fsSL "https://download.docker.com/linux/${distro}/gpg" |
    sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  sudo chmod a+r /etc/apt/keyrings/docker.gpg
  local codename
  codename="$(. /etc/os-release && echo "${VERSION_CODENAME:-}")"
  [ -z "$codename" ] && codename="$(lsb_release -cs 2>/dev/null || echo "")"
  printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/%s %s stable\n' \
    "$(dpkg --print-architecture)" "$distro" "$codename" |
    sudo tee /etc/apt/sources.list.d/docker.list >/dev/null
  sudo apt-get update -qq
  sudo apt-get install -y -qq \
    docker-ce docker-ce-cli containerd.io \
    docker-buildx-plugin docker-compose-plugin
}

# Linux RHEL/CentOS/Fedora/Rocky/Alma family — Docker's official rpm repo.
_docker_rpm() {
  local repo_distro="$1" # centos | fedora | rhel
  echo "Installing Docker Engine via dnf/yum (${repo_distro})..."
  local pm="yum"
  command -v dnf &>/dev/null && pm="dnf"
  sudo "$pm" install -y "${pm}-plugins-core" 2>/dev/null ||
    sudo "$pm" install -y yum-utils 2>/dev/null || true
  sudo "$pm" config-manager \
    --add-repo "https://download.docker.com/linux/${repo_distro}/docker-ce.repo"
  sudo "$pm" install -y \
    docker-ce docker-ce-cli containerd.io \
    docker-buildx-plugin docker-compose-plugin
}

# Linux: install Docker Engine natively using the host package manager.
install_docker_linux() {
  echo -e "${YELLOW}Installing Docker Engine (Linux)...${NC}"

  if command -v docker &>/dev/null && docker info &>/dev/null; then
    echo -e "${GREEN}Docker Engine already installed and running${NC}"
    return 0
  fi

  local distro
  distro="$(_linux_distro)"

  case "$distro" in
    ubuntu | debian | linuxmint | pop | elementary | kali | raspbian)
      _docker_apt "$distro"
      ;;
    fedora)
      _docker_rpm "fedora"
      ;;
    rhel | centos | rocky | almalinux)
      _docker_rpm "centos"
      ;;
    amzn)
      # Amazon Linux 2 / 2023 ships Docker in its own repo.
      sudo yum install -y docker
      ;;
    arch | manjaro | endeavouros)
      sudo pacman -Sy --noconfirm docker
      ;;
    alpine)
      sudo apk add --no-cache docker
      ;;
    *)
      echo -e "${RED}ERROR: Unsupported Linux distribution '${distro}'.${NC}" >&2
      echo "Install Docker manually: https://docs.docker.com/engine/install/" >&2
      exit 1
      ;;
  esac

  # Enable and start the Docker service.
  if command -v systemctl &>/dev/null && systemctl list-units --type=service &>/dev/null; then
    sudo systemctl enable docker
    sudo systemctl start docker
  elif command -v service &>/dev/null; then
    sudo service docker start
  fi

  # Add current user to the docker group for non-root access.
  local current_user="${USER:-$(id -un)}"
  if ! id -nG "$current_user" 2>/dev/null | grep -qw docker; then
    sudo usermod -aG docker "$current_user"
    echo -e "${YELLOW}Added '$current_user' to the docker group.${NC}"
    echo -e "${YELLOW}NOTE: Run 'newgrp docker' or re-login for the change to take effect.${NC}"
  fi

  _wait_for_docker
  echo -e "${GREEN}Docker Engine installed and running${NC}"
}

# Public entry point — routes to the right runtime for the current OS.
install_docker() {
  if [ "$OS" = "darwin" ]; then
    install_colima
  else
    install_docker_linux
  fi
}

# ---------------------------------------------------------------------------
# kind (headless local cluster — powers CI and the nightly e2e job)
# ---------------------------------------------------------------------------

install_kind() {
  echo -e "${YELLOW}Checking kind (minimum v${KIND_VERSION})...${NC}"

  if command -v kind &>/dev/null; then
    current_version=$(kind version 2>/dev/null |
      sed 's/.*kind v\([^ ]*\).*/\1/')
    if version_ge "$current_version" "${KIND_VERSION}"; then
      echo -e "${GREEN}kind v${current_version} already installed (meets minimum v${KIND_VERSION})${NC}"
      return 0
    fi
    echo -e "${YELLOW}kind v${current_version} is below the minimum v${KIND_VERSION} — installing a newer one${NC}"
  fi

  ensure_install_dir
  echo "Downloading kind v${KIND_VERSION} for ${OS}-${ARCH}..."
  curl -fsSLo /tmp/kind \
    "https://github.com/kubernetes-sigs/kind/releases/download/v${KIND_VERSION}/kind-${OS}-${ARCH}"
  chmod +x /tmp/kind
  sudo mv /tmp/kind "${INSTALL_DIR}/kind"

  installed=$(kind version 2>/dev/null | sed 's/.*kind v\([^ ]*\).*/\1/')
  if version_ge "$installed" "${KIND_VERSION}"; then
    echo -e "${GREEN}kind v${installed} installed and verified${NC}"
  else
    echo -e "${RED}ERROR: kind v${installed} still below the minimum v${KIND_VERSION} — is another kind earlier on PATH?${NC}" >&2
    exit 1
  fi
}

# ---------------------------------------------------------------------------
# Profile groups
# ---------------------------------------------------------------------------

install_common() {
  echo -e "${GREEN}========== Installing Common Tools ==========${NC}"
  install_kubectl
  echo -e "${GREEN}========== Common Tools Complete ==========${NC}\n"
}

install_k3d_profile() {
  echo -e "${GREEN}========== Installing K3D Profile ==========${NC}"
  install_common
  install_docker # Colima on macOS; Docker Engine on Linux
  install_k3d
  install_helm
  echo -e "${GREEN}Validating cluster details...${NC}"
  kubectl cluster-info 2>/dev/null ||
    echo -e "${YELLOW}Cluster not yet running — run 'make runtime-up' first${NC}"
  echo -e "${GREEN}========== K3D Profile Complete ==========${NC}\n"
}

install_kind_profile() {
  echo -e "${GREEN}========== Installing kind Profile ==========${NC}"
  install_common
  install_docker # Colima on macOS; Docker Engine on Linux
  install_kind
  install_helm
  echo -e "${GREEN}========== kind Profile Complete ==========${NC}\n"
}

# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

show_help() {
  cat <<EOF
Usage: setup-tools.sh [PROFILE]

Detected: OS=${OS}, ARCH=${ARCH}

Profiles:
  k3d     kubectl + colima/docker + k3d + helm    (local cluster — the golden path)
  kind    kubectl + colima/docker + kind + helm   (headless; used by CI)
  common  kubectl only
  all     all of the above

Container runtime installed per OS:
  macOS   → Colima (lightweight Docker VM via Homebrew)
  Linux   → Docker Engine (native; distro auto-detected)

Examples:
  ./setup-tools.sh k3d
  ./setup-tools.sh kind
  make setup-tools PROFILE=k3d
EOF
}

main() {
  local profile="${1:-k3d}"

  printf "${GREEN}╔════════════════════════════════════════════╗\n"
  printf "║  Setup Tools — Profile: %-6s (%s/%s)\n" "$profile" "$OS" "$ARCH"
  printf "╚════════════════════════════════════════════╝\n\n${NC}"

  case "$profile" in
    k3d) install_k3d_profile ;;
    kind) install_kind_profile ;;
    common) install_common ;;
    all)
      install_k3d_profile
      install_kind_profile
      ;;
    help | --help | -h)
      show_help
      exit 0
      ;;
    *)
      echo -e "${RED}Error: unknown profile '$profile'${NC}" >&2
      show_help
      exit 1
      ;;
  esac

  echo -e "${GREEN}Setup complete!${NC}"
}

main "$@"
