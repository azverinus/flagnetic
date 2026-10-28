import { connect } from "node:net";
import { afterEach, expect, test } from "vitest";
import { CrossOriginServer } from "./support/cross-origin-server.js";

const server = new CrossOriginServer();
afterEach(() => server.stop());

// A page that unloads mid-request, a beacon's included, closes the
// connection while its body is still read. The server drops that request
// instead of failing the whole run with an unhandled rejection.
test("a request the page abandons mid-body is dropped and the server keeps answering", async () => {
  const base = new URL(await server.start());
  await new Promise<void>((resolve, reject) => {
    const socket = connect(Number(base.port), base.hostname, () => {
      socket.write("POST /sdk/v1/events HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\n{\"half\":");
      setTimeout(() => {
        socket.destroy();
        resolve();
      }, 50);
    });
    socket.on("error", reject);
  });
  await new Promise((resolve) => setTimeout(resolve, 50));

  const res = await fetch(new URL("/sdk/v1/evaluate", base), { method: "POST" });
  expect(res.status).toBe(401);
  expect(server.requests().map((r) => r.path)).toEqual(["/sdk/v1/evaluate"]);
});
