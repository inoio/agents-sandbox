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
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.6.0/agents-sandbox-darwin-arm64"
      sha256 "34281ebbe3d504f38f36e53201396167553840e416fdd66270272a924690f7ce"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.6.0/agents-sandbox-linux-amd64"
      sha256 "db2a93d8cf63ed0fea7b7e4825a70e33711bcc62eb91af9ca11d179521f26d2e"
    end

    on_arm do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.6.0/agents-sandbox-linux-arm64"
      sha256 "9322c2e989fddf27ff9c8891db9a2b9aaa272f3f7916c8db78c6d3970667d99c"
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
