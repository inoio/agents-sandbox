class AgentsSandbox < Formula
  desc "Run coding agents in disposable microsandbox VMs"
  homepage "https://inoio.github.io/agents-sandbox/"
  license "GPL-3.0-or-later"

  livecheck do
    url :stable
    strategy :github_latest
  end

  on_macos do
    on_arm do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.2/agents-sandbox-darwin-arm64"
      sha256 "db565000e69bf075da8e98c0d596ecc09242bcec2fe7f23b3dcf180751398c62"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.2/agents-sandbox-linux-amd64"
      sha256 "26159b0ed213b8a3282c6796cdc18c451aaf71532ca784aa39e99825f3ee7cba"
    end

    on_arm do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.2/agents-sandbox-linux-arm64"
      sha256 "3214a17494caf8b3b65cb65f90aa712d2e21842d8015be15e3847cd17e34bcfe"
    end
  end

  def install
    binary = Pathname.glob("agents-sandbox-*").first
    chmod 0755, binary
    bin.install binary => "agents-sandbox"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/agents-sandbox version")
  end
end
