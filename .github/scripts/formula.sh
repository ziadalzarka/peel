#!/usr/bin/env bash
set -euo pipefail

version="${1:?usage: formula.sh <version> <dist-dir>}"
dist="${2:?usage: formula.sh <version> <dist-dir>}"

repo="ziadalzarka/peel"
base="https://github.com/$repo/releases/download/$version"

sha() {
	local archive="$dist/peel_${version}_$1.tar.gz"
	if [ ! -f "$archive" ]; then
		echo "formula.sh: missing archive $archive" >&2
		exit 1
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$archive" | cut -d' ' -f1
	else
		shasum -a 256 "$archive" | cut -d' ' -f1
	fi
}

darwin_arm64="$(sha darwin_arm64)"
darwin_amd64="$(sha darwin_amd64)"
linux_arm64="$(sha linux_arm64)"
linux_amd64="$(sha linux_amd64)"

cat <<RUBY
class Peel < Formula
  desc "Terminal diff reviewer that stages what you just reviewed"
  homepage "https://github.com/$repo"
  version "${version#v}"

  on_macos do
    if Hardware::CPU.arm?
      url "$base/peel_${version}_darwin_arm64.tar.gz"
      sha256 "$darwin_arm64"
    else
      url "$base/peel_${version}_darwin_amd64.tar.gz"
      sha256 "$darwin_amd64"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "$base/peel_${version}_linux_arm64.tar.gz"
      sha256 "$linux_arm64"
    else
      url "$base/peel_${version}_linux_amd64.tar.gz"
      sha256 "$linux_amd64"
    end
  end

  def install
    libexec.install "peel", "skills"
    bin.install_symlink libexec/"peel"
  end

  def caveats
    <<~EOS
      Claude Code reads peel's review comments through the bundled skill.
      Link it once:

        mkdir -p ~/.claude/skills
        ln -sfn #{opt_libexec}/skills/peel-review ~/.claude/skills/peel-review

      PR mode needs the "gh" CLI, and walkthroughs need "claude" or "codex".
      Run "peel providers" to see what is available.
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/peel version")
  end
end
RUBY
