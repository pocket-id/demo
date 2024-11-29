import { rateLimitRefillRate, rateLimitTokenCapacity } from "./config";

export class RateLimit {
  ipList: Record<string, TokenBucket> = {};

  registerRequest(ip: string) {
    let bucket = this.ipList[ip];
    if (!bucket) {
      bucket = new TokenBucket();
      this.ipList[ip] = bucket;
    }

    return bucket.remove();
  }
}

class TokenBucket {
  tokens = rateLimitTokenCapacity;

  constructor() {
    setInterval(() => this.add(), rateLimitRefillRate * 1000);
  }

  add() {
    const refilledTokensCount = this.tokens + 1;

    this.tokens =
      refilledTokensCount > rateLimitTokenCapacity
        ? rateLimitTokenCapacity
        : refilledTokensCount;
  }

  remove() {
    if (this.tokens > 0) {
      this.tokens--;

      return true;
    }

    return false;
  }
}
