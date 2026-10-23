// The digest that names the store keys of a connection.

/// MurmurHash3 x86 32-bit with seed 0 over [data], read as an unsigned
/// integer. Hashing UTF-16 code units instead of UTF-8 bytes, or reading the
/// result as signed, produces a different digest.
///
/// Every intermediate stays below 2^53, so the result is the same on the
/// Dart VM and when compiled to JavaScript.
int murmur3(List<int> data) {
  const c1 = 0xcc9e2d51;
  const c2 = 0x1b873593;
  final n = data.length;
  var h = 0;
  var i = 0;
  for (; i + 4 <= n; i += 4) {
    var k =
        (data[i] & 0xff) |
        (data[i + 1] & 0xff) << 8 |
        (data[i + 2] & 0xff) << 16 |
        (data[i + 3] & 0xff) << 24;
    k = _mul32(_rotl32(_mul32(k, c1), 15), c2);
    h = _rotl32(h ^ k, 13);
    h = (_mul32(h, 5) + 0xe6546b64) & 0xffffffff;
  }
  final rest = n - i;
  if (rest > 0) {
    var k = data[i] & 0xff;
    if (rest > 1) k |= (data[i + 1] & 0xff) << 8;
    if (rest > 2) k |= (data[i + 2] & 0xff) << 16;
    h ^= _mul32(_rotl32(_mul32(k, c1), 15), c2);
  }
  // murmur3 mixes the length modulo 2^32 by definition.
  h ^= n & 0xffffffff;
  h ^= h >>> 16;
  h = _mul32(h, 0x85ebca6b);
  h ^= h >>> 13;
  h = _mul32(h, 0xc2b2ae35);
  h ^= h >>> 16;
  return h;
}

/// a * b modulo 2^32 for unsigned 32-bit a and b, split so that no product
/// exceeds 2^53.
int _mul32(int a, int b) =>
    ((a & 0xffff) * b + ((((a >>> 16) * b) & 0xffff) << 16)) & 0xffffffff;

int _rotl32(int x, int r) => ((x << r) | (x >>> (32 - r))) & 0xffffffff;
