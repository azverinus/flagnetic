import 'dart:math';

/// How long to wait before the attempt after [failures] consecutive ones:
/// the bound starts at [base] and doubles with every further failure up to
/// [cap], and the wait is a random time up to that bound.
///
/// Full jitter, so a
/// platform coming back does not meet every client it lost at once.
Duration fullJitter(int failures, Duration base, Duration cap, Random random) {
  var bound = base;
  for (var i = 1; i < failures && bound < cap; i++) {
    bound *= 2;
  }
  if (bound > cap) bound = cap;
  return bound * random.nextDouble();
}
