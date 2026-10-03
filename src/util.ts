import fs from "fs";

export function getRandomInt(min, max) {
  min = Math.ceil(min);
  max = Math.floor(max);
  return Math.floor(Math.random() * (max - min + 1)) + min;
}

export function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * Get the content type of a URL
 * @param url
 */
export function contentType(url: string) {
  if (url.endsWith(".css")) {
    return "text/css";
  } else if (url.endsWith(".js")) {
    return "text/javascript";
  } else if (url.endsWith(".svg")) {
    return "image/svg+xml";
  } else if (url.endsWith(".woff2")) {
    return "font/woff2";
  } else if (url.endsWith(".woff")) {
    return "font/woff";
  } else {
    return "text/html";
  }
}

/**
 * Format a duration in seconds for people, like "10 minutes" or "1 hour"
 * @param seconds
 */
export function formatDuration(seconds: number) {
  const minutes = Math.round(seconds / 60);
  if (minutes >= 60 && minutes % 60 === 0) {
    const hours = minutes / 60;
    return `${hours} ${hours === 1 ? "hour" : "hours"}`;
  }
  return `${minutes} ${minutes === 1 ? "minute" : "minutes"}`;
}

export function fileExists(file: string) {
  return fs.promises
    .access(file, fs.constants.F_OK)
    .then(() => true)
    .catch(() => false);
}
