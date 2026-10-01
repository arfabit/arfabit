using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Reflection;
using System.Text;
using System.Windows.Forms;
using Microsoft.Win32;

namespace Arfabit.Shell
{
    // Shell runs ARFABIT as its child, and is its icon in the notification
    // area: a shortcut to the page that never does anything the page cannot
    // (docs/RELEASE.md §4).
    sealed class Shell : ApplicationContext
    {
        // What ARFABIT prints once it is serving, followed by its address
        // (docs/RELEASE.md §1).
        const string ReadyPrefix = "ARFABIT ready ";

        const int ExitStop = 0;
        const int ExitRestart = 75;

        static readonly string LogPath = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "arfabit", "arfabit.log");

        // Work arriving from other threads is run on this one through here.
        readonly Control ui = new Control();

        readonly NotifyIcon icon = new NotifyIcon();
        readonly ToolStripMenuItem status = new ToolStripMenuItem("Starting") { Enabled = false };
        readonly ToolStripMenuItem login = new ToolStripMenuItem("Start at login");

        readonly object logLock = new object();
        StreamWriter log;

        Process core;
        string url;
        Api api;

        // The page is opened once per run of the app, unless Hide on startup
        // is on, and not again after a restart: a page already open reconnects
        // by itself. Opening the app again while it runs always opens it.
        bool startedOnce;
        bool deciding;

        LoginItem.Report lastReport;
        readonly Timer loginTimer = new Timer { Interval = 30000 };

        // Start at login as this app last acted on it, and whether Windows has
        // had it on since. The app acts only when the setting changes: a change
        // made in Startup apps is the person's, and is followed, never undone.
        bool? loginSetting;
        bool sawLoginOn;

        public Shell()
        {
            ui.CreateControl();

            var menu = new ContextMenuStrip();
            menu.Items.Add(status);
            menu.Items.Add(new ToolStripSeparator());
            menu.Items.Add("Open ARFABIT", null, (s, e) => OpenPage());
            menu.Items.Add(login);
            menu.Items.Add(new ToolStripSeparator());
            menu.Items.Add("Restart ARFABIT", null, (s, e) => Post("api/restart", null));
            menu.Items.Add("Stop ARFABIT", null, (s, e) => Post("api/quit", null));
            menu.Opening += (s, e) => { if (api != null) api.Refresh(); };

            login.Click += (s, e) => Post("api/shell/settings",
                new Dictionary<string, object> { { "start_at_login", !login.Checked } });

            // A plain disc, light or dark to stand out on the taskbar, as the
            // Mac's menu bar shows one. It changes when the taskbar does.
            icon.Icon = TrayIcon();
            SystemEvents.UserPreferenceChanged += TaskbarMayHaveChanged;
            icon.Text = "ARFABIT";
            icon.ContextMenuStrip = menu;
            icon.MouseClick += (s, e) => { if (e.Button == MouseButtons.Left) OpenPage(); };

            // Turned off in Startup apps rather than on the page: noticed here
            // and reported, so the page never disagrees with Windows.
            loginTimer.Tick += (s, e) => Observe(LoginItem.Current());

            Start();
        }

        // TrayIcon is the disc that stands out on the taskbar as it is now:
        // dark unless the person has chosen a light one.
        static Icon TrayIcon()
        {
            var light = false;
            using (var key = Registry.CurrentUser.OpenSubKey(@"Software\Microsoft\Windows\CurrentVersion\Themes\Personalize"))
            {
                var value = key == null ? null : key.GetValue("SystemUsesLightTheme");
                light = value is int && (int)value == 1;
            }

            var name = light ? "tray-dark.ico" : "tray-light.ico";
            using (var stream = Assembly.GetExecutingAssembly().GetManifestResourceStream(name))
            {
                return new Icon(stream, SystemInformation.SmallIconSize);
            }
        }

        void TaskbarMayHaveChanged(object sender, UserPreferenceChangedEventArgs e)
        {
            if (e.Category != UserPreferenceCategory.General) return;
            ui.BeginInvoke(new Action(() =>
            {
                var old = icon.Icon;
                icon.Icon = TrayIcon();
                if (old != null) old.Dispose();
            }));
        }

        void Start()
        {
            url = null;
            OpenLog();

            var folder = Path.GetDirectoryName(Application.ExecutablePath);
            var start = new ProcessStartInfo(Path.Combine(folder, "core", "arfabit.exe"), "-no-open -shell")
            {
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                StandardOutputEncoding = Encoding.UTF8,
                StandardErrorEncoding = Encoding.UTF8,
                WorkingDirectory = folder,
            };

            var p = new Process { StartInfo = start, EnableRaisingEvents = true };
            p.OutputDataReceived += (s, e) => Received(e.Data);
            p.ErrorDataReceived += (s, e) => Received(e.Data);
            p.Exited += (s, e) =>
            {
                // Whatever was still on its way goes to the log first.
                p.WaitForExit();
                var code = p.ExitCode;
                ui.BeginInvoke(new Action(() => Exited(code)));
            };

            try
            {
                p.Start();
            }
            catch (Exception e)
            {
                MessageBox.Show(e.Message, "ARFABIT did not start", MessageBoxButtons.OK, MessageBoxIcon.Information);

                // Posted rather than called: on the first start the app's
                // message loop is not running yet, and would never end.
                ui.BeginInvoke(new Action(ExitThread));
                return;
            }
            p.BeginOutputReadLine();
            p.BeginErrorReadLine();
            core = p;
        }

        void OpenLog()
        {
            lock (logLock)
            {
                if (log != null) log.Dispose();
                Directory.CreateDirectory(Path.GetDirectoryName(LogPath));
                log = new StreamWriter(LogPath, true, new UTF8Encoding(false)) { AutoFlush = true };
            }
        }

        // Received is called with each line ARFABIT prints, on a background
        // thread, and null at the end.
        void Received(string line)
        {
            if (line == null) return;
            lock (logLock)
            {
                if (log != null) log.WriteLine(line);
            }

            if (line.StartsWith(ReadyPrefix))
            {
                var found = line.Substring(ReadyPrefix.Length).Trim();
                ui.BeginInvoke(new Action(() => Ready(found)));
            }
        }

        void Ready(string found)
        {
            if (url != null) return;
            url = found;

            api = new Api(found);
            api.Shell += state => ui.BeginInvoke(new Action(() => Follow(state)));
            api.Status += line => ui.BeginInvoke(new Action(() => status.Text = line));
            api.Connect();

            if (!startedOnce)
            {
                // Decided once the settings arrive, in Follow.
                startedOnce = true;
                deciding = true;
            }
            loginTimer.Start();
        }

        // Follow applies the settings ARFABIT keeps for the app.
        void Follow(Dictionary<string, object> state)
        {
            if (state == null) return;
            icon.Visible = Flag(state, "show_icon");
            if (deciding)
            {
                deciding = false;
                if (!Flag(state, "hide_on_start")) OpenPage();
            }
            object line;
            if (state.TryGetValue("status", out line) && line is string) status.Text = (string)line;

            var wanted = Flag(state, "start_at_login");
            if (loginSetting == null)
            {
                // The first word since the app opened. On in the setting but
                // not in Windows means it was taken off meanwhile, and the
                // setting follows.
                loginSetting = wanted;
                var now = LoginItem.Current();
                if (wanted && !now.Enabled)
                {
                    FollowSystem();
                    Report(now);
                }
                else
                {
                    Report(LoginItem.Apply(wanted));
                }
            }
            else if (loginSetting.Value == wanted)
            {
                // Not a change: an echo of a report, a refresh, or the other
                // setting changing.
                Observe(LoginItem.Current());
            }
            else
            {
                // Changed on the page or in the menu: the person asked.
                loginSetting = wanted;
                Report(LoginItem.Apply(wanted));
            }
        }

        // Observe notices the login item being removed or switched off in
        // Startup apps, and turns the setting off to match.
        void Observe(LoginItem.Report now)
        {
            if (loginSetting == true && sawLoginOn && !now.Enabled) FollowSystem();
            Report(now);
        }

        void FollowSystem()
        {
            loginSetting = false;
            if (api != null) api.Post("api/shell/settings", new Dictionary<string, object> { { "start_at_login", false } }, null);
        }

        static bool Flag(Dictionary<string, object> state, string name)
        {
            object value;
            return state.TryGetValue(name, out value) && value is bool && (bool)value;
        }

        // Report tells ARFABIT what Windows did with Start at login, when that
        // has changed.
        void Report(LoginItem.Report report)
        {
            login.Checked = report.Enabled;
            sawLoginOn = report.Enabled;
            if (report.Same(lastReport)) return;
            lastReport = report;
            if (api != null) api.Post("api/shell/login", report.Body(), null);
        }

        // OpenPageSoon opens the page from any thread: a second copy of the
        // app asked for it.
        public void OpenPageSoon()
        {
            ui.BeginInvoke(new Action(OpenPage));
        }

        void OpenPage()
        {
            if (url == null) return;
            Process.Start(new ProcessStartInfo(url) { UseShellExecute = true });
        }

        void Post(string path, Dictionary<string, object> body)
        {
            if (api == null) return;
            api.Post(path, body, (message, detail) =>
            {
                if (message == null) return;
                ui.BeginInvoke(new Action(() =>
                    MessageBox.Show(detail == null ? message : message + "\n\n" + detail, "ARFABIT",
                        MessageBoxButtons.OK, MessageBoxIcon.Information)));
            });
        }

        // Exited acts on ARFABIT's exit code (docs/RELEASE.md §1).
        void Exited(int code)
        {
            if (api != null) api.Close();
            api = null;
            core = null;
            loginTimer.Stop();
            lastReport = null;

            switch (code)
            {
                case ExitStop:
                    icon.Visible = false;
                    ExitThread();
                    return;

                case ExitRestart:
                    // Installing a staged update comes here, before starting
                    // again (docs/RELEASE.md §6). Not built yet.
                    status.Text = "Starting";
                    Start();
                    return;

                default:
                    // A stop nobody asked for is never silent, and never
                    // restarted.
                    icon.Visible = false;
                    var show = MessageBox.Show(
                        "What it said last is in its log. Open ARFABIT again to start it.\n\nShow the log?",
                        "ARFABIT stopped unexpectedly", MessageBoxButtons.YesNo, MessageBoxIcon.Information);
                    if (show == DialogResult.Yes)
                    {
                        Process.Start(new ProcessStartInfo(LogPath) { UseShellExecute = true });
                    }
                    ExitThread();
                    return;
            }
        }

        protected override void Dispose(bool disposing)
        {
            if (disposing)
            {
                SystemEvents.UserPreferenceChanged -= TaskbarMayHaveChanged;
                icon.Dispose();
                loginTimer.Dispose();
                ui.Dispose();
                lock (logLock)
                {
                    if (log != null) log.Dispose();
                    log = null;
                }
            }
            base.Dispose(disposing);
        }
    }
}
