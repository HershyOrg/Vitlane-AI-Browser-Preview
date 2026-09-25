const http = require("node:http");

const targetHost = process.argv[2];
const targetPort = Number(process.argv[3] || "18080");
const listenPort = Number(process.argv[4] || String(targetPort));

if (!targetHost || !Number.isInteger(targetPort) || !Number.isInteger(listenPort)) {
  throw new Error(
    "usage: node local-review-proxy.cjs <target-host> [target-port] [listen-port]",
  );
}

const server = http.createServer((request, response) => {
  const upstream = http.request(
    {
      hostname: targetHost,
      port: targetPort,
      method: request.method,
      path: request.url,
      headers: request.headers,
    },
    (upstreamResponse) => {
      response.writeHead(
        upstreamResponse.statusCode ?? 502,
        upstreamResponse.headers,
      );
      upstreamResponse.pipe(response);
    },
  );

  upstream.on("error", (error) => {
    if (!response.headersSent) {
      response.writeHead(502, {
        "content-type": "application/json; charset=utf-8",
      });
    }
    response.end(JSON.stringify({
      error: {
        code: "LOCAL_REVIEW_UPSTREAM_UNAVAILABLE",
        message: error.message,
      },
    }));
  });

  request.pipe(upstream);
});

server.on("error", (error) => {
  console.error(`Vitlane Windows localhost proxy failed: ${error.message}`);
  process.exitCode = 1;
});

server.listen(listenPort, "127.0.0.1", () => {
  console.log(
    `Vitlane Windows localhost proxy: 127.0.0.1:${listenPort} -> ${targetHost}:${targetPort}`,
  );
});
