using System;
using System.IO;
using System.Windows.Forms;
using Microsoft.Win32;

namespace Arfabit.Shell
{
    // InstalledSize keeps the size shown in Settings → Apps → Installed apps
    // true. The installer can only count the programs; the film list ARFABIT
    // downloads later is usually larger, and is removed by the uninstaller
    // too, so it is counted here.
    static class InstalledSize
    {
        // Where the installer registers ARFABIT: its AppId, with "_is1".
        const string UninstallKey = @"Software\Microsoft\Windows\CurrentVersion\Uninstall\{A7E05A9E-E19C-4AFF-9C7E-CC78085476B8}_is1";

        public static void Update()
        {
            try
            {
                var folder = Path.GetDirectoryName(Application.ExecutablePath);
                using (var key = Registry.CurrentUser.OpenSubKey(UninstallKey, true))
                {
                    // Not installed, or this is a copy run from somewhere
                    // else, such as a build: the installed size is not this
                    // copy's to change.
                    if (key == null) return;
                    var location = key.GetValue("InstallLocation") as string;
                    if (location == null || !SameFolder(location, folder)) return;

                    long bytes = 0;
                    foreach (var file in new DirectoryInfo(folder).GetFiles("*", SearchOption.AllDirectories))
                        bytes += file.Length;
                    var list = new FileInfo(Path.Combine(
                        Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "arfabit", "titles.json"));
                    if (list.Exists) bytes += list.Length;

                    key.SetValue("EstimatedSize", (int)(bytes / 1024), RegistryValueKind.DWord);
                }
            }
            catch (Exception)
            {
                // Only a number in Installed apps. It is tried again shortly.
            }
        }

        static bool SameFolder(string a, string b)
        {
            return string.Equals(
                Path.GetFullPath(a).TrimEnd('\\'),
                Path.GetFullPath(b).TrimEnd('\\'),
                StringComparison.OrdinalIgnoreCase);
        }
    }
}
