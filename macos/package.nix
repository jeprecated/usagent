{
  lib,
  stdenv,
  swift,
  swiftpm,
  version,
}:
stdenv.mkDerivation {
  pname = "usagent-bar";
  inherit version;

  src = lib.fileset.toSource {
    root = ./.;
    fileset = lib.fileset.unions [ ./Package.swift ./Info.plist ./Sources ./Tests ];
  };

  nativeBuildInputs = [ swift swiftpm ];

  # The swiftpm setup hook's build phase runs `swift build -c release` with
  # these flags, which also builds the UsageCoreChecks executable.
  swiftpmFlags = [ "--disable-sandbox" ];

  # nixpkgs' SwiftPM cannot run XCTest on Darwin, so the check phase runs the
  # same checks that `swift test` runs, through the UsageCoreChecks executable.
  doCheck = true;
  checkPhase = ''
    runHook preCheck
    "$(swiftpmBinPath)/UsageCoreChecks"
    runHook postCheck
  '';

  installPhase = ''
    runHook preInstall
    app="$out/Applications/UsageBar.app"
    mkdir -p "$app/Contents/MacOS"
    cp "$(swiftpmBinPath)/UsageBar" "$app/Contents/MacOS/UsageBar"
    cp Info.plist "$app/Contents/Info.plist"
    runHook postInstall
  '';

  # Sign after fixup has stripped the binary, so the signature stays valid.
  postFixup = ''
    /usr/bin/codesign --force --sign - "$out/Applications/UsageBar.app"
  '';

  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    test -x "$out/Applications/UsageBar.app/Contents/MacOS/UsageBar"
    /usr/bin/plutil -lint "$out/Applications/UsageBar.app/Contents/Info.plist"
    /usr/bin/codesign --verify --deep --strict "$out/Applications/UsageBar.app"
    runHook postInstallCheck
  '';

  meta = {
    description = "macOS menu bar app showing usagent's compact quota bar and detail popup";
    mainProgram = "UsageBar";
    platforms = lib.platforms.darwin;
  };
}
