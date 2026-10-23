/**
 * The number of buckets a unit is hashed into. A percentage in basis points
 * (0–10000) is compared against a bucket directly.
 */
export const BUCKETS = 10000;

/**
 * The bucket of a unit for a salt:
 *
 *     murmur3_32(utf8("{salt}.{unit_id}"), seed 0) mod 10000
 *
 * on the unsigned hash, giving 0–9999. A unit is inside percentage `p` when
 * its bucket is below `p`. It equals ClickHouse
 * `murmurHash3_32(concat(salt, '.', unit_id)) % 10000`.
 */
export function bucket(salt: string, unitId: string): number {
  return murmur3(`${salt}.${unitId}`) % BUCKETS;
}

const C1 = 0xcc9e2d51;
const C2 = 0x1b873593;

/**
 * MurmurHash3 x86 32-bit with seed 0 over UTF-8 bytes, read as an unsigned
 * integer. A string is hashed as its UTF-8 encoding (a lone surrogate as
 * U+FFFD, as TextEncoder does), never as UTF-16 code units: hashing UTF-16
 * or reading the result as signed produces different buckets and fails the
 * murmur3 vectors.
 */
export function murmur3(input: string | Uint8Array): number {
  const h = new Murmur3();
  if (typeof input === "string") {
    h.writeString(input);
  } else {
    for (const b of input) h.writeByte(b);
  }
  return h.sum();
}

/**
 * Streaming MurmurHash3 x86 32-bit, fed one byte at a time so a string is
 * encoded to UTF-8 on the fly without an intermediate buffer.
 */
class Murmur3 {
  private h = 0;
  private k = 0;
  private n = 0;

  writeString(s: string): void {
    for (let i = 0; i < s.length; i++) {
      let c = s.charCodeAt(i);
      if (c < 0x80) {
        this.writeByte(c);
        continue;
      }
      if (c < 0x800) {
        this.writeByte(0xc0 | (c >> 6));
        this.writeByte(0x80 | (c & 0x3f));
        continue;
      }
      if (c >= 0xd800 && c <= 0xdfff) {
        const next = i + 1 < s.length ? s.charCodeAt(i + 1) : 0;
        if (c <= 0xdbff && next >= 0xdc00 && next <= 0xdfff) {
          c = 0x10000 + ((c - 0xd800) << 10) + (next - 0xdc00);
          i++;
          this.writeByte(0xf0 | (c >> 18));
          this.writeByte(0x80 | ((c >> 12) & 0x3f));
          this.writeByte(0x80 | ((c >> 6) & 0x3f));
          this.writeByte(0x80 | (c & 0x3f));
          continue;
        }
        c = 0xfffd;
      }
      this.writeByte(0xe0 | (c >> 12));
      this.writeByte(0x80 | ((c >> 6) & 0x3f));
      this.writeByte(0x80 | (c & 0x3f));
    }
  }

  writeByte(b: number): void {
    this.k |= b << (8 * (this.n & 3));
    this.n++;
    if ((this.n & 3) === 0) {
      this.h ^= scramble(this.k);
      this.h = rotl(this.h, 13);
      this.h = (Math.imul(this.h, 5) + 0xe6546b64) | 0;
      this.k = 0;
    }
  }

  sum(): number {
    let h = this.h;
    if ((this.n & 3) !== 0) h ^= scramble(this.k);
    h ^= this.n;
    h ^= h >>> 16;
    h = Math.imul(h, 0x85ebca6b);
    h ^= h >>> 13;
    h = Math.imul(h, 0xc2b2ae35);
    h ^= h >>> 16;
    return h >>> 0;
  }
}

function scramble(k: number): number {
  return Math.imul(rotl(Math.imul(k, C1), 15), C2);
}

function rotl(x: number, r: number): number {
  return (x << r) | (x >>> (32 - r));
}
