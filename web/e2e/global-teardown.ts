import { stopDex } from "./support/dex";
import { stop } from "./support/instance";
import { stopMailpit } from "./support/mailpit";

export default async function globalTeardown(): Promise<void> {
  stop();
  await stopMailpit();
  await stopDex();
}
