using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Web.Script.Serialization;
using System.Windows.Forms;

namespace Arfabit.Shell
{
    // Updater installs a new version that ARFABIT has downloaded, checked and
    // staged (docs/RELEASE.md §6). On Windows the staged file is the new
    // installer: when ARFABIT exits with 75 the app runs it silently and
    // quits. The installer waits for the app's mutex, replaces both programs,
    // and starts the new app, which finishes the job: once its ARFABIT prints
    // the ready line the update is done. If it does not, the previous
    // installer, which every install keeps in its setup folder, is run to put
    // the old version back, and failed.json says what happened.
    static class Updater
    {
        // ARFABIT's staging folder, the same one it writes to
        // (internal/update, config.DefaultDir).
        static readonly string Folder = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "arfabit", "update");

        static string Staged { get { return Path.Combine(Folder, "staged.json"); } }
        static string InstallingFile { get { return Path.Combine(Folder, "installing.json"); } }
        static string Failed { get { return Path.Combine(Folder, "failed.json"); } }
        static string Rollback { get { return Path.Combine(Folder, "previous-setup.exe"); } }

        const string Silent = "/VERYSILENT /SUPPRESSMSGBOXES /NORESTART";

        static readonly JavaScriptSerializer Json = new JavaScriptSerializer();

        // An install under way, written by the old app for the new one.
        public sealed class Installing
        {
            public string Version;
        }

        // InstallStaged runs a staged installer, if there is one, and says
        // whether it did, in which case the app quits for it.
        public static bool InstallStaged()
        {
            var staged = Read(Staged);
            if (staged == null) return false;

            // Whatever happens next, this staging is used once.
            File.Delete(Staged);

            var version = Text(staged, "version");
            try
            {
                var setup = Path.Combine(Folder, Text(staged, "file"));

                // Kept so the old version can be put back.
                var previous = Path.Combine(Path.GetDirectoryName(Application.ExecutablePath), "setup", "ARFABIT-setup.exe");
                if (File.Exists(previous)) File.Copy(previous, Rollback, true);

                Write(InstallingFile, new Dictionary<string, object> { { "version", version } });
                Process.Start(new ProcessStartInfo(setup, Silent) { UseShellExecute = true });
                return true;
            }
            catch (Exception e)
            {
                File.Delete(InstallingFile);
                Fail(version, e.ToString());
                return false;
            }
        }

        // Finishing is the install this app is finishing, if it was just
        // installed.
        public static Installing Finishing()
        {
            var installing = Read(InstallingFile);
            if (installing == null) return null;
            return new Installing { Version = Text(installing, "version") };
        }

        // Succeeded is called once the new version's ARFABIT is ready.
        public static void Succeeded()
        {
            File.Delete(InstallingFile);
            File.Delete(Rollback);
            File.Delete(Failed);
        }

        // RollBack says the new version did not start, and runs the previous
        // installer to put the old one back. The app quits for it.
        public static void RollBack(Installing installing, string detail)
        {
            Fail(installing.Version, detail);
            File.Delete(InstallingFile);
            if (File.Exists(Rollback))
            {
                Process.Start(new ProcessStartInfo(Rollback, Silent) { UseShellExecute = true });
            }
        }

        static void Fail(string version, string detail)
        {
            Write(Failed, new Dictionary<string, object> { { "version", version }, { "detail", detail } });
        }

        static Dictionary<string, object> Read(string path)
        {
            try
            {
                return Json.Deserialize<Dictionary<string, object>>(File.ReadAllText(path, Encoding.UTF8));
            }
            catch (Exception)
            {
                return null;
            }
        }

        static string Text(Dictionary<string, object> d, string key)
        {
            object value;
            return d.TryGetValue(key, out value) ? value as string ?? "" : "";
        }

        static void Write(string path, Dictionary<string, object> value)
        {
            Directory.CreateDirectory(Folder);
            var temp = path + ".tmp";
            File.WriteAllText(temp, Json.Serialize(value), new UTF8Encoding(false));
            if (File.Exists(path)) File.Delete(path);
            File.Move(temp, path);
        }
    }
}
