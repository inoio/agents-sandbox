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
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.3/agents-sandbox-darwin-arm64"
      sha256 "8ef6d4642d3d8bcf5fdd65ca62a678d580a80cb54187595c3c4242f9bd667f42"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.3/agents-sandbox-linux-amd64"
      sha256 "74abf83520a7037308613be9d2b88766165923e53831bfe67ec7eaf388f12ca0"
    end

    on_arm do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.3/agents-sandbox-linux-arm64"
      sha256 "b759e9b2e8cdbcc2c4b7a09b99a875ff334d058a5cfadf3d0d26fdb9af25f9eb"
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
