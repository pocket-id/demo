import "dotenv/config";
import ejs from "ejs";
import fs from "fs/promises";
import * as http from "http";
import gracefulShutdown from "http-graceful-shutdown";
import httpProxy from "http-proxy";
import path from "path";
import {
  entryPath,
  installURL,
  serverPort,
  showEntry,
  trustProxy,
  websiteName,
} from "./config";
import { Pool, PublicPoolError } from "./pool";
import { contentType, sleep } from "./util";

// Catch unexpected errors here
const unexpectedErrorHandler = (error: unknown) => {
  console.trace(error);
};
process.addListener("unhandledRejection", unexpectedErrorHandler);
process.addListener("uncaughtException", unexpectedErrorHandler);

const pool = new Pool();
const proxy = httpProxy.createProxyServer();

await pool.clearInstance();

const server = http.createServer(async (req, res) => {
  try {
    await requestHandler(req, res);
  } catch (e) {
    console.error(e);
    res.writeHead(500);
    res.end("Internal server error");
  }
});

console.log(`Listening on port ${serverPort}`);
server.listen(serverPort);

gracefulShutdown(server, {
  signals: "SIGINT SIGTERM",
  timeout: 30000, // timeout: 30 secs
  development: false, // not in dev mode
  forceExit: true, // triggers process.exit() at the end of shutdown process
  onShutdown: shutdownFunction, // shutdown function (async) - e.g. for cleanup DB, ...
  finally: finalFunction, // finally function (sync) - e.g. for logging
});

/**
 * Get session ID from the first part of the host
 * @param req
 */
function getSessionID(req: http.IncomingMessage) {
  const sessionId = req.headers.host?.split(".")[0]?.replace("demo-", "");
  if (sessionId?.length !== 8) {
    return null;
  }
  return sessionId;
}

async function requestHandler(
  req: http.IncomingMessage,
  res: http.ServerResponse,
) {
  if (!req.url) {
    res.end("No url");
    return;
  }

  const sessionID = getSessionID(req);
  const target = pool.getServiceURL(sessionID);
  // Handle request
  if (req.url === "/" && !(sessionID && target)) {
    res.writeHead(302, {
      Location: "/start-demo",
    });

    res.end();
  } else if (req.url === "/start-demo" || req.url === "/start-demo") {
    res.writeHead(200, { "Content-Type": "text/html" });
    const indexTemplate = ejs.render(
      await fs.readFile("./src/views/index.ejs", "utf-8"),
      {
        websiteName,
        installURL,
        autoStart: !showEntry,
        entryPath,
      },
    );
    res.end(indexTemplate);
  } else if (req.url.startsWith("/demo-kuma/")) {
    if (req.url === "/demo-kuma/start-instance") {
      try {
        const { endSessionTime, sessionID } = await pool.startInstance(
          getIPAddress(req),
          req.headers.host!,
        );
        res.writeHead(200, {
          "Content-Type": "application/json",
        });
        res.end(
          JSON.stringify({
            ok: true,
            sessionID,
            endSessionTime,
          }),
        );
      } catch (e) {
        console.error(e);
        res.writeHead(500, { "Content-Type": "application/json" });
        res.end(
          JSON.stringify({
            ok: false,
            error: e instanceof PublicPoolError ? e.message : undefined,
          }),
        );
      }
    } else if (req.url === "/demo-kuma/validate-session") {
      const sessionID = getSessionID(req);

      res.writeHead(200, {
        "Content-Type": "application/json",
      });

      res.end(
        JSON.stringify({
          ok: pool.sessionList[sessionID] !== undefined,
        }),
      );
    } else {
      try {
        const data = await fs.readFile(path.join("./public", req.url));
        res.writeHead(200, { "Content-Type": contentType(req.url) });
        res.end(data);
      } catch (e) {
        res.writeHead(404);
        res.end("Not found");
      }
    }
  } else {
    await proxyWeb(req, res);
  }
}

async function proxyWeb(
  req: http.IncomingMessage,
  res: http.ServerResponse,
  retryCount = 0,
) {
  const sessionID = getSessionID(req);
  const target = pool.getServiceURL(sessionID);

  if (sessionID && target) {
    proxy.web(
      req,
      res,
      {
        target,
      },
      async (err) => {
        if (retryCount <= 10) {
          await sleep(2000);
          await proxyWeb(req, res, retryCount + 1);
        } else {
          res.writeHead(500);
          res.end("Unable to connect to the instance");
        }
      },
    );
  } else {
    res.writeHead(404);
    res.end("Session not found");
  }
}

async function shutdownFunction(signal: string | undefined) {
  console.info("Shutdown requested");
  console.info("server", "Called signal: " + signal);
  await pool.clearInstance();
}

/**
 * Final function called before application exits
 */
function finalFunction() {
  console.info("Graceful shutdown successful!");
}

function getIPAddress(req: http.IncomingMessage) {
  const realIpFromHeader =
    req.headers["x-forwarded-for"] || req.headers["x-real-ip"];

  if (trustProxy && realIpFromHeader) {
    return realIpFromHeader as string;
  }

  const { remoteAddress } = req.socket;
  if (!remoteAddress) throw Error("Can't determine IP address");

  return remoteAddress;
}
