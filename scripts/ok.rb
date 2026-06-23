# Homebrew formula for OK — the world's first autonomous dev agent platform
# with OS-level sandbox, ProofChain audit, 30+ languages, and DAG multi-agent.
#
# Install:
#   brew tap colbymchenry/ok
#   brew install ok
#
# Update sha256 after release:
#   shasum -a 256 dist/ok-darwin-arm64 | awk '{print $1}' | pbcopy  # macOS ARM
#   shasum -a 256 dist/ok-darwin-amd64 | awk '{print $1}' | pbcopy  # macOS Intel
#   shasum -a 256 dist/ok-linux-arm64  | awk '{print $1}'           # Linux ARM
#   shasum -a 256 dist/ok-linux-amd64  | awk '{print $1}'           # Linux Intel
class Ok < Formula
  desc "World's-first autonomous dev agent — OS sandbox, ProofChain, DAG multi-agent, 30 languages"
  homepage "https://github.com/colbymchenry/ok"
  version "0.9.7"
  license "MIT"

  if OS.mac?
    if Hardware::CPU.arm?
      url "https://github.com/colbymchenry/ok/releases/download/v#{version}/ok-darwin-arm64"
      sha256 "" # shasum -a 256 dist/ok-darwin-arm64
    else
      url "https://github.com/colbymchenry/ok/releases/download/v#{version}/ok-darwin-amd64"
      sha256 "" # shasum -a 256 dist/ok-darwin-amd64
    end
  elsif OS.linux?
    if Hardware::CPU.arm?
      url "https://github.com/colbymchenry/ok/releases/download/v#{version}/ok-linux-arm64"
      sha256 "" # shasum -a 256 dist/ok-linux-arm64
    else
      url "https://github.com/colbymchenry/ok/releases/download/v#{version}/ok-linux-amd64"
      sha256 "" # shasum -a 256 dist/ok-linux-amd64
    end
  end

  def install
    bin.install "ok-#{OS.kernel_name.downcase}-#{Hardware::CPU.arch}" => "ok"
  end

  test do
    system "#{bin}/ok", "--version"
  end
end
