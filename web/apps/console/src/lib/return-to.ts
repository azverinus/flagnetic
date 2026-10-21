/**
 * Where sign-in sends a member afterwards. Only a path on this origin is
 * accepted, so a link cannot use the sign-in page to bounce someone to
 * another site.
 */
export function safeReturnTo(value: string | undefined): string {
  if (!value || !value.startsWith("/")) return "/";
  // A browser drops tab and newline from a URL, so "/\t/host" would turn
  // protocol-relative; no control character belongs in a path anyway.
  if ([...value].some(isControl)) return "/";
  // "//host" and "/\host" are protocol-relative: another origin.
  if (value.startsWith("//") || value.startsWith("/\\")) return "/";
  return value;
}

/** The path, query and fragment of a location, as `returnTo` carries it. */
export function locationOf(location: {
  pathname: string;
  searchStr?: string;
  hash?: string;
}): string {
  const search = location.searchStr ?? "";
  const hash = location.hash ?? "";
  return `${location.pathname}${search}${hash ? (hash.startsWith("#") ? hash : `#${hash}`) : ""}`;
}

function isControl(character: string): boolean {
  const code = character.charCodeAt(0);
  return code < 0x20 || code === 0x7f;
}
