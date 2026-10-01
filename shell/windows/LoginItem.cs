using System;
using System.Collections.Generic;
using System.Windows.Forms;
using Microsoft.Win32;

namespace Arfabit.Shell
{
    // LoginItem starts the app when the person logs in, from a value under
    // their own Run key. It is what Task Manager → Startup apps and Settings →
    // Apps → Startup list and switch off (docs/RELEASE.md §3).
    static class LoginItem
    {
        const string RunKey = @"Software\Microsoft\Windows\CurrentVersion\Run";

        // Where Windows records that the person switched a startup app off.
        // Only read: that choice is theirs, and the page says where it is.
        const string ApprovedKey = @"Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run";

        const string ValueName = "ARFABIT";

        // What Windows did, sent to ARFABIT as it is (internal/web/shell.go).
        // State is "enabled", "disabled" (switched off in Startup apps), or
        // "notRegistered".
        public sealed class Report
        {
            public bool Enabled;
            public string State;
            public string Detail;

            public Dictionary<string, object> Body()
            {
                var body = new Dictionary<string, object> { { "enabled", Enabled }, { "state", State } };
                if (Detail != null) body["detail"] = Detail;
                return body;
            }

            public bool Same(Report other)
            {
                return other != null && other.Enabled == Enabled && other.State == State && other.Detail == Detail;
            }
        }

        // Apply makes the Run key match the setting, and says what Windows made
        // of it.
        public static Report Apply(bool wanted)
        {
            string detail = null;
            try
            {
                using (var key = Registry.CurrentUser.CreateSubKey(RunKey))
                {
                    if (wanted)
                    {
                        key.SetValue(ValueName, "\"" + Application.ExecutablePath + "\"");
                    }
                    else if (key.GetValue(ValueName) != null)
                    {
                        key.DeleteValue(ValueName, false);
                    }
                }
            }
            catch (Exception e)
            {
                detail = e.ToString();
            }

            var report = Current();
            report.Detail = detail;
            return report;
        }

        // Current says what Windows has now.
        public static Report Current()
        {
            string command = null;
            using (var key = Registry.CurrentUser.OpenSubKey(RunKey))
            {
                if (key != null) command = key.GetValue(ValueName) as string;
            }
            if (command == null)
            {
                return new Report { Enabled = false, State = "notRegistered" };
            }

            // Windows keeps a few bytes per app here once it has been switched
            // in Startup apps. An odd first byte means switched off.
            byte[] approved = null;
            using (var key = Registry.CurrentUser.OpenSubKey(ApprovedKey))
            {
                if (key != null) approved = key.GetValue(ValueName) as byte[];
            }
            if (approved != null && approved.Length > 0 && (approved[0] & 1) == 1)
            {
                return new Report { Enabled = false, State = "disabled" };
            }

            return new Report { Enabled = true, State = "enabled" };
        }
    }
}
