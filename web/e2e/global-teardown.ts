import { stop } from "./support/instance";

export default function globalTeardown(): void {
  stop();
}
