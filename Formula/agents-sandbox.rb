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
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.1/agents-sandbox-darwin-arm64"
      sha256 "61c43f74cf95534705f84fd2772425fb83d1cb2fd5e92cf8632ca4477199c569"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.1/agents-sandbox-linux-amd64"
      sha256 "b5df965d9b54e83cc0cf2a0e959ed3fe1fcb9a47dac8d6d70cf45cde8311b017"
    end

    on_arm do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.1/agents-sandbox-linux-arm64"
      sha256 "04c43aa679e646c5f68bf130dd6ef537700f16032cb5151bf297bfc8ff0e1bbc"
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
