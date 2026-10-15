; This script is the inno setup script for glab. 
; Must be executed after `make build` which creates an executable in the ./bin directory
;
; `iscc /dVersion="<version_here>" "setup_windows.iss"`

#define MyAppName "glab"
#define MyAppPublisher "GitLab"
#define MyAppURL "https://gitlab.com/gitlab-org/cli"

#ifndef Version
  #define Version "DEV"
#endif

#ifndef ExeName
  #define ExeName "glab.exe"
#endif

; Arch is the suffix used in the installer filename. Pass via
; `iscc -DArch=arm64` to build the ARM64 installer; defaults to the
; legacy x86_64 build so direct `iscc` invocations keep working.
#ifndef Arch
  #define Arch "x86_64"
#endif

; Each architecture gets its own AppId so Windows tracks the installs as
; distinct products. That's enables safer update path without the risk of 
; mixing up artifacts from distinct architectures
#if Arch == "arm64"
  #define AppGuid "{{43F62295-C40A-4D12-A520-9F46AD177207}"
#else
  #define AppGuid "{{679C50AE-F48A-46D1-9493-77F095E9884D}"
#endif

[Setup]
; NOTE: The value of AppId uniquely identifies this application. Do not use the same AppId value in installers for other applications.
; (To generate a new GUID, click Tools | Generate GUID inside the IDE.)
AppId={#AppGuid}
AppName={#MyAppName}
AppVersion={#Version}
AppVerName={#MyAppName} v{#Version}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
LicenseFile=LICENSE
; Uncomment the following line to run in non administrative install mode (install for current user only.)
;PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=commandline
OutputDir=bin
OutputBaseFilename=glab_{#Version}_Windows_{#Arch}_installer
Compression=lzma
SolidCompression=yes
WizardStyle=modern dynamic
; Restrict the ARM64 installer to ARM64 hosts. The x86_64 installer is
; intentionally left unrestricted so it continues to run on ARM64 under
; x64 emulation for users who pick it explicitly. Both installers run in
; 64-bit install mode so {autopf} resolves to "Program Files" instead of
; "Program Files (x86)".
#if Arch == "arm64"
ArchitecturesAllowed=arm64
ArchitecturesInstallIn64BitMode=arm64
#else
ArchitecturesInstallIn64BitMode=x64compatible
#endif

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "armenian"; MessagesFile: "compiler:Languages\Armenian.isl"
Name: "brazilianportuguese"; MessagesFile: "compiler:Languages\BrazilianPortuguese.isl"
Name: "catalan"; MessagesFile: "compiler:Languages\Catalan.isl"
Name: "corsican"; MessagesFile: "compiler:Languages\Corsican.isl"
Name: "czech"; MessagesFile: "compiler:Languages\Czech.isl"
Name: "danish"; MessagesFile: "compiler:Languages\Danish.isl"
Name: "dutch"; MessagesFile: "compiler:Languages\Dutch.isl"
Name: "finnish"; MessagesFile: "compiler:Languages\Finnish.isl"
Name: "french"; MessagesFile: "compiler:Languages\French.isl"
Name: "german"; MessagesFile: "compiler:Languages\German.isl"
Name: "hebrew"; MessagesFile: "compiler:Languages\Hebrew.isl"
Name: "icelandic"; MessagesFile: "compiler:Languages\Icelandic.isl"
Name: "italian"; MessagesFile: "compiler:Languages\Italian.isl"
Name: "japanese"; MessagesFile: "compiler:Languages\Japanese.isl"
Name: "norwegian"; MessagesFile: "compiler:Languages\Norwegian.isl"
Name: "polish"; MessagesFile: "compiler:Languages\Polish.isl"
Name: "portuguese"; MessagesFile: "compiler:Languages\Portuguese.isl"
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"
Name: "slovak"; MessagesFile: "compiler:Languages\Slovak.isl"
Name: "slovenian"; MessagesFile: "compiler:Languages\Slovenian.isl"
Name: "spanish"; MessagesFile: "compiler:Languages\Spanish.isl"
Name: "turkish"; MessagesFile: "compiler:Languages\Turkish.isl"
Name: "ukrainian"; MessagesFile: "compiler:Languages\Ukrainian.isl"

[Files]
Source: "bin\{#ExeName}"; DestDir: "{app}"; Flags: ignoreversion
; NOTE: Don't use "Flags: ignoreversion" on any shared system files

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#ExeName}"

[Code]
function BoolToStr(B: Boolean; const TrueStr, FalseStr: string): string;
begin
  if B then
    Result := TrueStr
  else
    Result := FalseStr;
end;

procedure GetRegPath(var RegRootKey: Integer; var RegRootKeyStr, RegSubkeyPath: string);
begin
  if IsAdminInstallMode then
    begin
      RegRootKey := HKEY_LOCAL_MACHINE;
      RegRootKeyStr := 'HKEY_LOCAL_MACHINE';
      RegSubkeyPath := 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment';
    end
  else
    begin
      RegRootKey := HKEY_CURRENT_USER;
      RegRootKeyStr := 'HKEY_CURRENT_USER';
      RegSubkeyPath := 'Environment';
    end;
end;

function PosIgnoreCase(SubStr, S: AnyString): Integer;
begin
  Result := Pos(AnsiUppercase(SubStr), AnsiUppercase(S));
end;

function QuotePathIfNeeded(const Path: string): string;
begin
  // If path contains semicolon, enclose it in double quotes
  if Pos(';', Path) > 0 then
    Result := '"' + Path + '"'
  else
    Result := Path;
end;

procedure AddPath;
var
  Existed: Boolean;
  AppPath: string;
  OrigPath: string;
  NewPath: string;
  RegRootKey: Integer;
  RegRootKeyStr: string;
  RegSubkeyPath: string;
begin
  AppPath := QuotePathIfNeeded(ExpandConstant('{app}'));
  GetRegPath(RegRootKey, RegRootKeyStr, RegSubkeyPath);

  LogFmt('Adding application path %s to %s PATH', [AppPath, BoolToStr(IsAdminInstallMode, 'system', 'user')]);

  // Read existing PATH value, use empty value if it doesn't exist
  Existed := RegValueExists(RegRootKey, RegSubkeyPath, 'Path');
  if Existed then
    begin
      if not RegQueryStringValue(RegRootKey, RegSubkeyPath, 'Path', OrigPath) then begin
        LogFmt('[ERROR] Failed to read registry key %s\%s\Path', [RegRootKeyStr, RegSubkeyPath]);
        MsgBox('Failed to read PATH environment variable from registry. ' +
               'You may need to manually add: ' + AppPath, mbError, MB_OK);
        exit;
      end;
    end
  else
    begin
      OrigPath := '';
    end;

  // Check if path already contains the application path
  if PosIgnoreCase(';' + AppPath + ';', ';' + OrigPath + ';') > 0 then begin
    LogFmt('Application path already in registry key %s\%s\Path, skipping update', [RegRootKeyStr, RegSubkeyPath]);
    exit;
  end;

  if Length(OrigPath) = 0 then
    NewPath := AppPath
  else
    begin
      if Copy(OrigPath, Length(OrigPath), 1) = ';' then
        NewPath := OrigPath + AppPath
      else
        NewPath := OrigPath + ';' + AppPath;
    end;

  if RegWriteExpandStringValue(RegRootKey, RegSubkeyPath, 'Path', NewPath) then
    begin
      if Existed then
        LogFmt('Registry key %s\%s\Path updated successfully', [RegRootKeyStr, RegSubkeyPath])
      else
        LogFmt('Registry key %s\%s\Path created successfully', [RegRootKeyStr, RegSubkeyPath]);
    end
  else
    begin
      LogFmt('[ERROR] Failed to write to registry key %s\%s\Path', [RegRootKeyStr, RegSubkeyPath])
      MsgBox('Failed to update PATH environment variable. ' +
             'You may need to manually add: ' + AppPath, mbError, MB_OK);
    end;
end;

procedure RemovePath;
var
  AppPathSemicolons: string;
  AppPath: string;
  OrigPath: string;
  NewPath: string;
  RegRootKey: Integer;
  RegRootKeyStr: string;
  RegSubkeyPath: string;
  Index: Integer;
begin
  AppPath := QuotePathIfNeeded(ExpandConstant('{app}'));
  AppPathSemicolons := ';' + AppPath + ';';
  GetRegPath(RegRootKey, RegRootKeyStr, RegSubkeyPath);

  LogFmt('Removing application path %s from %s PATH', [AppPath, BoolToStr(IsAdminInstallMode, 'system', 'user')]);

  if not RegQueryStringValue(RegRootKey, RegSubkeyPath, 'Path', OrigPath) then begin
    LogFmt('[ERROR] Failed to read registry key %s\%s\Path', [RegRootKeyStr, RegSubkeyPath]);
    exit;
  end;

  NewPath := ';' + OrigPath + ';';
  Index := PosIgnoreCase(AppPathSemicolons, NewPath);

  if Index > 0 then
    begin
      // The PATH contains application path, remove it with surrounding semicolons
      Delete(NewPath, Index, Length(AppPathSemicolons));

      // Re-balance semicolons in the PATH after deleting ";{app};" from ";PATH;"
      if Index = 1 then
        begin
          // Removed app path from the beginning, so remove only trailing
          // semicolon, our leading one has been removed already
          if Length(NewPath) > 0 then Delete(NewPath, Length(NewPath), 1);
        end
      else if (Index > 1) and (Index < Length(NewPath)) then
        begin
          // Removed app path from the middle of the string, so re-insert now
          // missing semicolon and remove both the leading and trailing semicolons
          Insert(';', NewPath, Index);
          Delete(NewPath, Length(NewPath), 1);
          Delete(NewPath, 1, 1);
        end
      else
        begin
          // Removed app path from the end, so remove only the leading semicolon
          Delete(NewPath, 1, 1);
        end;

      if RegWriteExpandStringValue(RegRootKey, RegSubkeyPath, 'Path', NewPath) then
        LogFmt('Registry key %s\%s\Path updated successfully', [RegRootKeyStr, RegSubkeyPath])
      else
        begin
          LogFmt('[ERROR] Failed to write registry key %s\%s\Path', [RegRootKeyStr, RegSubkeyPath]);
          MsgBox('Failed to update PATH environment variable during uninstallation. ' +
                 'You may need to manually remove: ' + AppPath, mbError, MB_OK);
        end;
    end
  else
    LogFmt('Application path not found in registry key %s\%s\Path, no cleanup needed', [RegRootKeyStr, RegSubkeyPath]);
end;

#if Arch != "arm64"
// Releases before the x86_64 installer switched to 64-bit install mode
// registered their uninstaller in the 32-bit registry view, which 64-bit
// mode does not search for a previous install. Remove that install first
// so upgrades do not leave a second copy and PATH entry behind.
procedure UninstallLegacy32BitModeInstall;
var
  UninstallKey: string;
  UninstallString: string;
  ResultCode: Integer;
begin
  if not (IsAdminInstallMode and Is64BitInstallMode) then
    exit;

  UninstallKey := ExpandConstant('Software\Microsoft\Windows\CurrentVersion\Uninstall\{#AppGuid}_is1');
  if not RegQueryStringValue(HKLM32, UninstallKey, 'UninstallString', UninstallString) then
    exit;

  LogFmt('Uninstalling previous 32-bit mode install with %s', [UninstallString]);
  if not Exec(RemoveQuotes(UninstallString), '/VERYSILENT /SUPPRESSMSGBOXES /NORESTART', '',
              SW_HIDE, ewWaitUntilTerminated, ResultCode) then
    LogFmt('[ERROR] Failed to run previous uninstaller: %s', [SysErrorMessage(ResultCode)])
  else if ResultCode <> 0 then
    LogFmt('[ERROR] Previous uninstaller exited with code %d', [ResultCode]);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  UninstallLegacy32BitModeInstall();
  Result := '';
end;
#endif

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then begin
    AddPath();
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then begin
    RemovePath();
  end;
end;