using System;
using System.Collections.Generic;
using System.IO;
using System.Net;
using System.Text;
using System.Threading;
using System.Web.Script.Serialization;

namespace Arfabit.Shell
{
    // Api talks to ARFABIT through its web API and live events, exactly as the
    // page does. The app knows nothing about discs or jobs.
    //
    // Its events are raised on background threads; the shell moves them to its
    // own.
    sealed class Api
    {
        readonly string baseUrl;
        readonly JavaScriptSerializer json = new JavaScriptSerializer();
        volatile bool closed;
        volatile HttpWebRequest stream;

        // The settings ARFABIT keeps for the app, and the status line
        // (internal/web/shell.go).
        public event Action<Dictionary<string, object>> Shell;
        public event Action<string> Status;

        public Api(string url)
        {
            baseUrl = url.TrimEnd('/');
        }

        // Connect fetches the settings, then listens for changes to them.
        public void Connect()
        {
            Refresh();
            new Thread(Listen) { IsBackground = true, Name = "ARFABIT events" }.Start();
        }

        // Close stops listening, for an ARFABIT that has stopped.
        public void Close()
        {
            closed = true;
            var request = stream;
            if (request != null) request.Abort();
        }

        // Refresh asks for the settings and the status line again.
        public void Refresh()
        {
            ThreadPool.QueueUserWorkItem(_ =>
            {
                try
                {
                    var request = (HttpWebRequest)WebRequest.Create(baseUrl + "/api/shell");
                    request.Timeout = 5000;
                    using (var response = request.GetResponse())
                    using (var reader = new StreamReader(response.GetResponseStream(), Encoding.UTF8))
                    {
                        var state = json.Deserialize<Dictionary<string, object>>(reader.ReadToEnd());
                        var handler = Shell;
                        if (handler != null && state != null) handler(state);
                    }
                }
                catch (Exception)
                {
                    // ARFABIT is stopping or starting; the next event or
                    // refresh catches up.
                }
            });
        }

        // Post sends a request, and calls back with ARFABIT's refusal in its
        // own words, or nulls when it was done.
        public void Post(string path, Dictionary<string, object> body, Action<string, string> done)
        {
            ThreadPool.QueueUserWorkItem(_ =>
            {
                string message = null, detail = null;
                try
                {
                    var request = (HttpWebRequest)WebRequest.Create(baseUrl + "/" + path);
                    request.Method = "POST";
                    request.Timeout = 10000;
                    request.ContentType = "application/json";
                    var bytes = Encoding.UTF8.GetBytes(body == null ? "" : json.Serialize(body));
                    request.ContentLength = bytes.Length;
                    using (var s = request.GetRequestStream()) s.Write(bytes, 0, bytes.Length);
                    using (request.GetResponse()) { }
                }
                catch (WebException e)
                {
                    message = "ARFABIT did not do that.";
                    detail = e.Message;
                    var response = e.Response as HttpWebResponse;
                    if (response != null)
                    {
                        using (response)
                        using (var reader = new StreamReader(response.GetResponseStream(), Encoding.UTF8))
                        {
                            try
                            {
                                var reply = json.Deserialize<Dictionary<string, object>>(reader.ReadToEnd());
                                object m, d;
                                if (reply != null && reply.TryGetValue("message", out m)) message = m as string;
                                detail = reply != null && reply.TryGetValue("detail", out d) ? d as string : null;
                            }
                            catch (Exception) { }
                        }
                    }
                }
                catch (Exception e)
                {
                    message = "ARFABIT did not answer.";
                    detail = e.Message;
                }
                if (done != null) done(message, detail);
            });
        }

        void Listen()
        {
            while (!closed)
            {
                try
                {
                    var request = (HttpWebRequest)WebRequest.Create(baseUrl + "/events");
                    request.Accept = "text/event-stream";
                    request.Timeout = Timeout.Infinite;
                    request.ReadWriteTimeout = Timeout.Infinite;
                    stream = request;

                    using (var response = request.GetResponse())
                    using (var reader = new StreamReader(response.GetResponseStream(), Encoding.UTF8))
                    {
                        string name = "", data = "", line;
                        while (!closed && (line = reader.ReadLine()) != null)
                        {
                            // Events are separated by a blank line.
                            if (line.Length == 0)
                            {
                                Handle(name, data);
                                name = data = "";
                            }
                            else if (line.StartsWith("event: ")) name = line.Substring(7);
                            else if (line.StartsWith("data: ")) data += line.Substring(6);
                        }
                    }
                }
                catch (Exception)
                {
                    // Dropped while ARFABIT runs: listen again shortly.
                }

                if (closed) return;
                Thread.Sleep(2000);
                Refresh();
            }
        }

        void Handle(string name, string data)
        {
            try
            {
                if (name == "shell")
                {
                    var handler = Shell;
                    if (handler != null) handler(json.Deserialize<Dictionary<string, object>>(data));
                }
                else if (name == "status")
                {
                    var reply = json.Deserialize<Dictionary<string, object>>(data);
                    object line;
                    var handler = Status;
                    if (handler != null && reply.TryGetValue("line", out line)) handler(line as string);
                }
            }
            catch (Exception)
            {
                // An event the app cannot read is one it does not need.
            }
        }
    }
}
