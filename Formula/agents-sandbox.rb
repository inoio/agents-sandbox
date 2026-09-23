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
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.3.0/agents-sandbox-darwin-arm64"
      sha256 "086bfeac441055c083254ef8a3a805b9dd91741ef4b74855d6a87d8ecbcae679"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.3.0/agents-sandbox-linux-amd64"
      sha256 "6beaf4a1e509cba909a308cd7e76d265ce8b054af7cc529d45b50bf415ca7996"
    end

    on_arm do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.3.0/agents-sandbox-linux-arm64"
      sha256 "8dd7e282c21f0959f14fb418cc85176c38ede2c6eaa3376f1e2babd0a5633c64"
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
