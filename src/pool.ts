import crypto from "crypto";
import fs from "fs";
import childProcessAsync from "promisify-child-process";
import {
  dockerNetwork,
  entryPath,
  httpsEnabled,
  maxSessions,
  serviceName,
  servicePort,
  sessionTime,
  stackPrefix,
  startTimeout,
} from "./config";
import { RateLimit } from "./rate-limit";
import { sleep } from "./util";

export class Pool {
  /**
   * sessionList[sessionID] = serviceURL
   */
  sessionList: Record<string, string> = {};
  rateLimit = new RateLimit();

  async startInstance(ip: string, host: string) {
    if (!this.rateLimit.registerRequest(ip)) {
      throw new PublicPoolError(
        "Your IP has reached the limit of demos. Please try again later."
      );
    }

    if (Object.keys(this.sessionList).length >= maxSessions) {
      throw new PublicPoolError(
        "Maximum number of sessions reached. Please try again later.",
      );
    }

    let sessionID: string = "";

    while (true) {
      sessionID = crypto.randomUUID().substring(0, 8);
      if (this.sessionList[sessionID] === undefined) {
        break;
      }
    }

    const hostWithoutPort = host.replace(/:\d+$/, "");
    const appUrl = `${
      httpsEnabled ? "https" : "http"
    }://${sessionID}.${hostWithoutPort}`;

    const envVariables = {
      PUBLIC_APP_URL: appUrl,
    };
    const envFileContent = Object.entries(envVariables)
      .map(([k, v]) => `${k}=${v}`)
      .join("\n");

    const envFilePath = `.env-${sessionID}`;

    await fs.promises.writeFile(envFilePath, envFileContent);

    console.log(`[${sessionID}] Start a session from ${ip}`);

    await childProcessAsync.spawn(
      "docker",
      [
        "compose",
        "--file",
        "compose-demo.yaml",
        "--env-file",
        envFilePath,
        "-p",
        `${stackPrefix}-${sessionID}`,
        "up",
        "-d",
      ],
      {
        encoding: "utf-8",
      },
    );

    fs.promises.unlink(envFilePath);

    const startStackTime = Date.now();
    let baseURL = "";

    // Wait until the service is opened
    while (true) {
      try {
        const ip = await this.getServiceIP(sessionID);
        baseURL = `http://${ip}:${servicePort}`;
        const entryURL = baseURL + entryPath;

        console.log("Checking entry: " + entryURL);

        // Try to access the index page
        const res = await fetch(entryURL);
        await res.text();

        if (res.status === 200) {
          break;
        }
      } catch (e) {}

      await sleep(2000);
      if (Date.now() - startStackTime > startTimeout * 1000) {
        throw new Error("Start instance timeout");
      }
    }

    const endSessionTime = Date.now() + sessionTime * 1000;

    // Timer for closing the session
    setTimeout(async () => {
      console.log(`[${sessionID}] Time's up`);
      await this.stopInstance(sessionID);
      delete this.sessionList[sessionID];
    }, sessionTime * 1000);

    this.sessionList[sessionID] = baseURL;
    console.log(`[${sessionID}] Session started`);

    return {
      sessionID,
      endSessionTime,
    };
  }

  async stopInstance(sessionID: string) {
    await childProcessAsync.spawn(
      "docker",
      [
        "compose",
        "-f",
        "compose-demo.yaml",
        "-p",
        `${stackPrefix}-${sessionID}`,
        "down",
        "--volumes",
        "--remove-orphans",
      ],
      {
        encoding: "utf-8",
        env: {
          ...process.env,
        },
      },
    );
  }

  getServiceURL(sessionID: string | null): string | undefined {
    if (!sessionID) {
      return undefined;
    }
    return this.sessionList[sessionID];
  }

  async getServiceIP(sessionID: string): Promise<string> {
    const response = await childProcessAsync.spawn(
      "docker",
      [
        "inspect",
        `${stackPrefix}-${sessionID}-${serviceName}-1`,
        "--format",
        "json",
      ],
      {
        encoding: "utf-8",
      },
    );

    if (typeof response.stdout !== "string") {
      throw new Error("No output");
    }

    const array = JSON.parse(response.stdout);

    if (!Array.isArray(array)) {
      throw new Error("Not an array");
    }

    if (array.length === 0) {
      throw new Error("Array is empty");
    }

    const obj = array[0];

    // Check if the object is valid
    if (!obj || typeof obj !== "object") {
      throw new Error("Not an object");
    }

    const networkSettings = obj.NetworkSettings;

    if (!networkSettings) {
      throw new Error("No network settings");
    }

    const networks = obj.NetworkSettings.Networks;

    if (!networks) {
      throw new Error("No networks");
    }

    // Find the target network
    const network = networks[dockerNetwork];

    if (!network) {
      throw new Error("Network not found");
    }

    const ip = network.IPAddress;

    if (!ip) {
      throw new Error("IP not found");
    }

    return ip;
  }

  async clearInstance() {
    const result = await childProcessAsync.spawn(
      "docker",
      ["compose", "ls", "--format", "json"],
      {
        encoding: "utf-8",
      },
    );

    if (typeof result.stdout === "string") {
      const list = JSON.parse(result.stdout);
      for (const stack of list) {
        if (stack.Name?.startsWith(stackPrefix + "-")) {
          console.log(`Clearing ${stack.Name}`);
          const result = await childProcessAsync.spawn(
            "docker",
            [
              "compose",
              "--file",
              "compose-demo.yaml",
              "-p",
              stack.Name,
              "down",
              "--volumes",
              "--remove-orphans",
            ],
            {
              encoding: "utf-8",
            },
          );

          console.log(result.stdout, result.stderr);
        }
      }
    }
  }
}

export class PublicPoolError extends Error {
  constructor(message = "", ...args) {
    super(message, ...args);
    this.message = message;
  }
}
