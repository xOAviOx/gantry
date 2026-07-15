// Minimal zero-dependency Node web app for Gantry deploy demos.
// Serves /healthz (for the deploy health-check) and / (prints env + hostname).
const http = require("http");
const os = require("os");

const PORT = process.env.PORT || 3000;
const GREETING = process.env.GREETING || "hello from gantry";
const VERSION = process.env.APP_VERSION || "1";

const server = http.createServer((req, res) => {
  if (req.url === "/healthz") {
    res.writeHead(200, { "Content-Type": "text/plain" });
    res.end("ok");
    return;
  }
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end(
    [
      GREETING,
      `version: ${VERSION}`,
      `hostname: ${os.hostname()}`,
      `port: ${PORT}`,
      `node: ${process.version}`,
      "",
    ].join("\n"),
  );
});

server.listen(PORT, () => {
  console.log(`hello-node listening on :${PORT} (version ${VERSION})`);
});

for (const sig of ["SIGTERM", "SIGINT"]) {
  process.on(sig, () => {
    console.log(`received ${sig}, shutting down`);
    server.close(() => process.exit(0));
  });
}
