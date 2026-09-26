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
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.3.1/agents-sandbox-darwin-arm64"
      sha256 "79bd80082344befae575eb3046647998bd6f91718455a31091ecc08644949973"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.3.1/agents-sandbox-linux-amd64"
      sha256 "7bdef95a2ff4fc20724f72c841a16e0b13cf4d398e955f351dfd95e6ea50d5cb"
    end

    on_arm do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.3.1/agents-sandbox-linux-arm64"
      sha256 "185a2b9a77f7034484d86332f04789a595c22c4a9e3d91e391e260682c60099b"
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
