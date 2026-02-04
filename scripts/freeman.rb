class Freeman < Formula
  desc "High-performance streaming TTS server using Kokoro-82M"
  homepage "https://github.com/yourname/freeman"
  url "https://github.com/yourname/freeman/releases/download/v1.0.0-draft/freeman-darwin-arm64"
  sha256 "REPLACE_WITH_ACTUAL_SHA256" # Run `shasum -a 256 <binary>`
  version "1.0.0-draft"

  def install
    bin.install "freeman-darwin-arm64" => "freeman"
  end

  test do
    system "#{bin}/freeman", "--version"
  end

  def caveats
    <<~EOS
      Freeman requires espeak-ng to be installed for phonetization:
        brew install espeak-ng
    EOS
  end
end
