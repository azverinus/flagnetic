import { stopDex } from "./support/dex";
import { stop } from "./support/instance";

export default async function globalTeardown(): Promise<void> {
  stop();
  await stopDex();
}
