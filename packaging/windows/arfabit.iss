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
; The shell holds this while it runs. Nothing is replaced until it is gone.
AppMutex=ARFABIT-shell
WizardStyle=modern
Compression=lzma2
SolidCompression=yes
OutputDir={#Dist}
OutputBaseFilename=ARFABIT-setup

[Messages]
SetupAppRunningError=ARFABIT is running. Stop it from its icon or from Settings on its page, then press OK.
UninstallAppRunningError=ARFABIT is running. Stop it from its icon or from Settings on its page, then press OK.

[Files]
Source: "{#Dist}\ARFABIT.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#Dist}\core\arfabit.exe"; DestDir: "{app}\core"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\ARFABIT"; Filename: "{app}\ARFABIT.exe"

[Run]
; Opens ARFABIT when done, after an update too.
Filename: "{app}\ARFABIT.exe"; Description: "Open ARFABIT"; Flags: nowait postinstall

[Code]
// The shell writes this value when Start at login is on. It goes with the
// programs; nothing else of ARFABIT's is touched.
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
    RegDeleteValue(HKEY_CURRENT_USER, 'Software\Microsoft\Windows\CurrentVersion\Run', 'ARFABIT');
end;
