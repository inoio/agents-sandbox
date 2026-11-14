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
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.0/agents-sandbox-darwin-arm64"
      sha256 "03c9a8eca273c1cb37774e2d1a806318e4b4c6509ca8d786a2b690c1ed338f39"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.0/agents-sandbox-linux-amd64"
      sha256 "9cf2e429f07197bc7c824d3e775d6385ab84f65982a578d453e4820f74d60858"
    end

    on_arm do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.0/agents-sandbox-linux-arm64"
      sha256 "f1a1f83ab9235ec499b822fbea8fcf2ed05741d4fabced329c80359accdb5d97"
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
