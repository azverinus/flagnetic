import { stopDex } from "./support/dex";
import { stop } from "./support/instance";
import { stopKeycloak } from "./support/keycloak";
import { stopMailpit } from "./support/mailpit";

export default async function globalTeardown(): Promise<void> {
  stop();
  await stopMailpit();
  await stopKeycloak();
  await stopDex();
}
