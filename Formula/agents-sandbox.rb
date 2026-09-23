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
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.2.0/agents-sandbox-darwin-arm64"
      sha256 "6e1d39409ec80d8dd73afcbf93a971acac1f54adb6ceb8aa65f02363530b29de"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.2.0/agents-sandbox-linux-amd64"
      sha256 "8fb9ad28d239ba11f7b16b93c267156e1efbfedf181be9f97203eb33814fff86"
    end

    on_arm do
      url "https://github.com/inoio/agents-sandbox/releases/download/v0.2.0/agents-sandbox-linux-arm64"
      sha256 "16d9d0a7bf48453d9dfc5f0ec06d986fb00057711a63545fe8f775d95cad2c95"
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
