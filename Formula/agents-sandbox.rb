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
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.5.0/agents-sandbox-darwin-arm64"
      sha256 "34466bb8a1e31b30ce6ba94840e626eb5e9c0a1bdc46d43f5d9e8b35f2402f51"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.5.0/agents-sandbox-linux-amd64"
      sha256 "0d1d506531e0846202d5601bda4b79b2e18c4845f8a0c6b43659e3496296829e"
    end

    on_arm do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.5.0/agents-sandbox-linux-arm64"
      sha256 "763c2095736367cd2a1dbd89be8df8eed2856ee8500bf766419baee9594debb3"
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
