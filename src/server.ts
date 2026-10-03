import crypto from "crypto";
import "dotenv/config";
import ejs from "ejs";
import fs from "fs/promises";
import * as http from "http";
import gracefulShutdown from "http-graceful-shutdown";
import httpProxy from "http-proxy";
import path from "path";
import {
  entryPath,
  serverPort,
  sessionTime,
  showEntry,
  trustProxy,
} from "./config";
import { Pool, PublicPoolError } from "./pool";
import { contentType, formatDuration, sleep } from "./util";

// Catch unexpected errors here
const unexpectedErrorHandler = (error: unknown) => {
  console.trace(error);
};
process.addListener("unhandledRejection", unexpectedErrorHandler);
process.addListener("uncaughtException", unexpectedErrorHandler);

const pool = new Pool();
const proxy = httpProxy.createProxyServer();

// The stylesheet's URL carries a hash of its content, so caches like Cloudflare's fetch it again after a change
const styleVersion = crypto
  .createHash("sha256")
  .update(await fs.readFile("./public/demo-kuma/main.css"))
  .digest("hex")
  .substring(0, 8);

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
  const sessionId = req.headers.host?.split(".")[0];
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
  // Handle request
  if (req.url === "/" && !sessionID) {
    res.writeHead(302, {
      Location: "/start-demo",
    });

    res.end();
  } else if (req.url === "/start-demo" || req.url === "/start-demo") {
    await renderView(res, 200, "index", {
      autoStart: !showEntry,
      entryPath,
      sessionDuration: formatDuration(sessionTime),
    });
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
        const filePath = req.url.split("?")[0];
        const data = await fs.readFile(path.join("./public", filePath));
        res.writeHead(200, { "Content-Type": contentType(filePath) });
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
          await sendError(req, res, 500, "Unable to connect to the instance", {
            title: "The demo isn't responding",
            text: "Your Pocket ID instance didn't answer in time. Start a new demo to try again.",
            actionText: "Start a new demo",
            actionURL: startDemoURL(req),
          });
        }
      },
    );
  } else if (sessionID) {
    await sendError(req, res, 404, "Session not found", {
      title: "This demo has ended",
      text: `Demo instances are deleted after ${formatDuration(sessionTime)}, together with everything in them. Start a new one to keep exploring Pocket ID.`,
      actionText: "Start a new demo",
      actionURL: startDemoURL(req),
    });
  } else {
    await sendError(req, res, 404, "Not found", {
      title: "Page not found",
      text: "There's nothing here. Start a demo to try Pocket ID.",
      actionText: "Go to the demo",
      actionURL: "/start-demo",
    });
  }
}

async function renderView(
  res: http.ServerResponse,
  status: number,
  view: string,
  data: Record<string, unknown>,
) {
  const html = await ejs.renderFile(`./src/views/${view}.ejs`, {
    styleVersion,
    ...data,
  });
  res.writeHead(status, { "Content-Type": "text/html" });
  res.end(html);
}

/**
 * Show browsers a page in the Pocket ID branding, and answer everything else, like the app's API requests, in plain text
 */
async function sendError(
  req: http.IncomingMessage,
  res: http.ServerResponse,
  status: number,
  message: string,
  page: { title: string; text: string; actionText: string; actionURL: string },
) {
  if (req.headers.accept?.includes("text/html")) {
    await renderView(res, status, "message", page);
  } else {
    res.writeHead(status);
    res.end(message);
  }
}

/**
 * The start page lives on the main domain, so a session subdomain links back to it without the session ID
 * @param req
 */
function startDemoURL(req: http.IncomingMessage) {
  const sessionID = getSessionID(req);
  if (!sessionID) {
    return "/start-demo";
  }
  const mainHost = req.headers.host!.substring(sessionID.length + 1);
  return `//${mainHost}/start-demo`;
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
