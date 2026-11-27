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
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.4/agents-sandbox-darwin-arm64"
      sha256 "b98102d868c805c6a8bc35fa33dd063e27068154fed84a4905e894c68e31060d"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.4/agents-sandbox-linux-amd64"
      sha256 "e10ce56d3df76426f0e238ffb7854c92d712223f45e61438ae52877b52b10042"
    end

    on_arm do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.7.4/agents-sandbox-linux-arm64"
      sha256 "28c6d4042a46a9af33bc3db989973e7d2894906f665d2e066437a34c18f209a6"
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
