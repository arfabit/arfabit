using System;
using System.Threading;
using System.Windows.Forms;

namespace Arfabit.Shell
{
    // ARFABIT.exe: the notification area app around ARFABIT, the Go program
    // (docs/RELEASE.md §3). It starts core\arfabit.exe, opens the page once
    // ARFABIT says where it is, shows the icon, keeps the login item, and
    // starts ARFABIT again when it exits asking to be. It knows nothing about
    // discs.
    static class Program
    {
        // One per person logged in. The installer waits for it to be released
        // before replacing anything (docs/RELEASE.md §6).
        public const string MutexName = "ARFABIT-shell";

        // Set by a second copy, to have the running one open the page.
        const string OpenSignalName = "ARFABIT-shell-open";

        [STAThread]
        static void Main()
        {
            bool first;
            using (var mutex = new Mutex(true, MutexName, out first))
            {
                if (!first)
                {
                    // Opened again while running: the running copy opens the
                    // page, whether its icon is shown or hidden.
                    EventWaitHandle signal;
                    if (EventWaitHandle.TryOpenExisting(OpenSignalName, out signal))
                    {
                        signal.Set();
                        signal.Dispose();
                    }
                    return;
                }

                using (var open = new EventWaitHandle(false, EventResetMode.AutoReset, OpenSignalName))
                {
                    Application.EnableVisualStyles();
                    Application.SetCompatibleTextRenderingDefault(false);

                    var shell = new Shell();
                    ThreadPool.RegisterWaitForSingleObject(open, (state, timedOut) => shell.OpenPageSoon(), null, Timeout.Infinite, false);
                    Application.Run(shell);
                }

                mutex.ReleaseMutex();
            }
        }
    }
}
