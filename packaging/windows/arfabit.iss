; ARFABIT-setup.exe, built with Inno Setup 6 (docs/RELEASE.md §5).
;
; Installs ARFABIT for the person running it, with no administrator password:
; the shell, ARFABIT.exe, and ARFABIT itself, core\arfabit.exe, in
; %LOCALAPPDATA%\Programs\ARFABIT. The same installer, run silently, is the
; update (§6). Uninstalling removes the programs and the login item, never
; the library, the originals, or ARFABIT's own settings and state (§8).
;
; Built by packaging/windows/build.ps1, which passes Version and Dist.

#ifndef Version
  #define Version "0.0.0"
#endif
#ifndef Dist
  #define Dist "..\..\dist\windows"
#endif

[Setup]
; Never change this: Windows knows an installed ARFABIT by it.
AppId={{A7E05A9E-E19C-4AFF-9C7E-CC78085476B8}
AppName=ARFABIT
AppVersion={#Version}
AppVerName=ARFABIT {#Version}
AppPublisher=ARFABIT
PrivilegesRequired=lowest
DefaultDirName={autopf}\ARFABIT
DisableDirPage=yes
DisableProgramGroupPage=yes
DisableReadyPage=yes
UninstallDisplayName=ARFABIT
UninstallDisplayIcon={app}\ARFABIT.exe
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
WizardStyle=modern
Compression=lzma2
SolidCompression=yes
OutputDir={#Dist}
OutputBaseFilename=ARFABIT-setup
SetupIconFile={#Dist}\icons\ARFABIT.ico

[Files]
Source: "{#Dist}\ARFABIT.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Dist}\core\arfabit.exe"; DestDir: "{app}\core"; Flags: ignoreversion
; This installer, kept so a later update that does not start can put this
; version back (docs/RELEASE.md §6).
Source: "{srcexe}"; DestDir: "{app}\setup"; DestName: "ARFABIT-setup.exe"; Flags: external ignoreversion

[Icons]
Name: "{autoprograms}\ARFABIT"; Filename: "{app}\ARFABIT.exe"

[Run]
; Opens ARFABIT when done, after an update too.
Filename: "{app}\ARFABIT.exe"; Description: "Open ARFABIT"; Flags: nowait postinstall

[Code]
// The app holds the mutex ARFABIT-shell while it runs, and nothing is
// replaced or removed until it is gone. An update is run silently by the app
// as it quits, so a silent run waits for it; anyone running this by hand is
// asked to stop ARFABIT first.
function ShellGone(Silent: Boolean; Text: String): Boolean;
var
  Tries: Integer;
begin
  Tries := 0;
  while CheckForMutexes('ARFABIT-shell') do
  begin
    if Silent then
    begin
      if Tries >= 60 then
      begin
        Result := False;
        exit;
      end;
      Sleep(500);
      Tries := Tries + 1;
    end
    else if MsgBox(Text, mbInformation, MB_OKCANCEL) = IDCANCEL then
    begin
      Result := False;
      exit;
    end;
  end;
  Result := True;
end;

function InitializeSetup(): Boolean;
begin
  Result := ShellGone(WizardSilent(), 'ARFABIT is running. Stop it from its icon or from Settings on its page, then press OK.');
end;

function InitializeUninstall(): Boolean;
begin
  Result := ShellGone(UninstallSilent(), 'ARFABIT is running. Stop it from its icon or from Settings on its page, then press OK.');
end;

// The shell writes this value when Start at login is on. It goes with the
// programs; nothing else of ARFABIT's is touched.
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
    RegDeleteValue(HKEY_CURRENT_USER, 'Software\Microsoft\Windows\CurrentVersion\Run', 'ARFABIT');
end;
