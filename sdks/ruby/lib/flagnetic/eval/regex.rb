# frozen_string_literal: true

require_relative "unicode_tables"
require_relative "regex_vm"

module Flagnetic
  # Eval is the evaluator; see flagnetic/eval.rb.
  module Eval
    # Regex compiles the pattern of a matches condition. Patterns are RE2
    # syntax, as the reference evaluator (Go's regexp) reads them. Ruby's own
    # engine reads a different dialect (^ and $ are line anchors, \s includes
    # \v, (?m) means what RE2 calls (?s), (?i) folds ß to ss, lookarounds and
    # backreferences exist) and backtracks, so a context value an end user
    # chooses could make it burn CPU or, cut off by a time limit, answer
    # differently from the server. So a pattern is never handed to Regexp: it
    # is parsed as RE2, compiled to the program of a Pike VM and matched in
    # time linear in the input.
    #
    # The policy:
    #
    # - Every RE2 construct is supported with RE2 semantics: literals and
    #   escapes, classes with ranges, POSIX and Perl classes (ASCII, as in
    #   RE2), \p and \P with Go's category and script names, groups (plain,
    #   non-capturing, named), flags i, m, s and U, alternation, greedy and
    #   lazy repetition up to 1000, \A, \z, \b, \B and \Q...\E. Case folding
    #   uses Go's simple folding orbits, not Ruby's.
    # - What RE2 rejects is rejected: lookarounds, backreferences, atomic
    #   groups, possessive or nested repetition, \Z, \G, \h, \K, \R, \C,
    #   comments, unknown escapes and flags, repeat counts above 1000 (also
    #   when nested repeats multiply past it), groups nested deeper than 1000
    #   and programs of more than Go's size limit. The condition is then
    #   inconsistent_ruleset, as in every other evaluator.
    # - \p and \P under (?i) match the property's code points and every
    #   code point sharing a folding orbit with one, as Go does; whether a
    #   code point has the property is Ruby's Unicode data, the orbits are
    #   Go's.
    # - A match searches anywhere in the string unless the pattern is
    #   anchored. Every byte of invalid UTF-8 in the matched string reads as
    #   one U+FFFD, as Go decodes it.
    module Regex
      # Error is a pattern outside RE2 syntax or outside RE2.
      class Error < StandardError; end

      REPLACEMENT = [0xFFFD].pack("U")
      private_constant :REPLACEMENT

      # Pattern is a compiled matches pattern.
      class Pattern
        def initialize(program)
          @program = program
          freeze
        end

        # match? reports whether s contains a match.
        def match?(str)
          str = str.scrub { |bad| REPLACEMENT * bad.bytesize } unless str.valid_encoding?
          Vm.search(@program, str.codepoints)
        end
      end

      # compile parses an RE2 pattern into a Pattern, raising Error when it
      # is not one this evaluator can match with RE2 semantics.
      def self.compile(pattern)
        Pattern.new(Compiler.compile(Parser.new(pattern).parse))
      rescue RegexpError => e
        raise Error, e.message
      end

      MAX_RUNE = 0x10FFFF
      SURROGATES = (0xD800..0xDFFF)
      WORD = [[0x30, 0x39], [0x41, 0x5A], [0x5F, 0x5F], [0x61, 0x7A]].freeze
      PERL_CLASSES = {
        "d" => [[0x30, 0x39]],
        "s" => [[0x09, 0x0A], [0x0C, 0x0D], [0x20, 0x20]],
        "w" => WORD
      }.freeze
      POSIX_CLASSES = {
        "alnum" => [[0x30, 0x39], [0x41, 0x5A], [0x61, 0x7A]],
        "alpha" => [[0x41, 0x5A], [0x61, 0x7A]],
        "ascii" => [[0x00, 0x7F]],
        "blank" => [[0x09, 0x09], [0x20, 0x20]],
        "cntrl" => [[0x00, 0x1F], [0x7F, 0x7F]],
        "digit" => [[0x30, 0x39]],
        "graph" => [[0x21, 0x7E]],
        "lower" => [[0x61, 0x7A]],
        "print" => [[0x20, 0x7E]],
        "punct" => [[0x21, 0x2F], [0x3A, 0x40], [0x5B, 0x60], [0x7B, 0x7E]],
        "space" => [[0x09, 0x0D], [0x20, 0x20]],
        "upper" => [[0x41, 0x5A]],
        "word" => WORD,
        "xdigit" => [[0x30, 0x39], [0x41, 0x46], [0x61, 0x66]]
      }.freeze
      SIMPLE_ESCAPES = { "a" => 0x07, "f" => 0x0C, "n" => 0x0A, "r" => 0x0D, "t" => 0x09, "v" => 0x0B }.freeze
      PROPERTY_NAMES = (["Any"] + UnicodeTables::PROPERTY_NAMES).to_h { |n| [n, true] }.freeze
      MAX_REPEAT = 1000
      MAX_DEPTH = 1000
      # Go's limit on the size of a compiled expression, in instructions.
      MAX_SIZE = (128 << 20) / 40
      NO_NEWLINE = [[0, 9], [0xB, MAX_RUNE]].freeze
      ANYTHING = [[0, MAX_RUNE]].freeze

      FOLD = 1
      MULTILINE = 2
      DOT_NL = 4

      ORBIT_OF = UnicodeTables::ORBITS.each_with_object({}) do |orbit, h|
        orbit.each { |c| h[c] = orbit }
      end.freeze

      FOLD_EXTRA = {} # rubocop:disable Style/MutableConstant
      FOLD_EXTRA_LOCK = Mutex.new
      private_constant :FOLD_EXTRA, :FOLD_EXTRA_LOCK

      # Sets are lists of inclusive [lo, hi] code point ranges.
      module Sets
        module_function

        def normalize(ranges)
          merged = []
          ranges.sort.each do |lo, hi|
            if merged.empty? || lo > merged.last[1] + 1
              merged << [lo, hi]
            elsif hi > merged.last[1]
              merged.last[1] = hi
            end
          end
          merged
        end

        def complement(ranges)
          out = []
          next_lo = 0
          normalize(ranges).each do |lo, hi|
            out << [next_lo, lo - 1] if lo > next_lo
            next_lo = hi + 1
          end
          out << [next_lo, MAX_RUNE] if next_lo <= MAX_RUNE
          out
        end

        # fold adds every code point sharing a simple case folding orbit with
        # one already in ranges.
        def fold(ranges)
          if ranges.size == 1 && ranges[0][0] == ranges[0][1]
            same = ORBIT_OF[ranges[0][0]]
            return same ? normalize(same.map { |c| [c, c] }) : ranges
          end

          out = ranges.dup
          UnicodeTables::ORBITS.each do |orbit|
            next unless orbit.any? { |c| ranges.any? { |lo, hi| c.between?(lo, hi) } }

            orbit.each { |c| out << [c, c] }
          end
          normalize(out)
        end

        # property is the class of the code points with property name, or
        # without it when negated, and, under (?i), those an orbit of simple
        # case folding ties to one that has it. Go adds the same code points
        # (its FoldCategory and FoldScript tables).
        def property(name, negated, fold)
          Property.new(name, negated, fold && name != "Any" ? fold_extra(name) : [])
        end

        # fold_extra lists the code points without property name that share
        # a folding orbit with one that has it.
        def fold_extra(name)
          FOLD_EXTRA_LOCK.synchronize do
            FOLD_EXTRA[name] ||= begin
              has = Regexp.new("\\A\\p{#{name}}\\z")
              UnicodeTables::ORBITS.flat_map do |orbit|
                members = orbit.map { |c| [c, has.match?([c].pack("U"))] }
                members.any?(&:last) ? members.reject(&:last).map(&:first) : []
              end.sort.freeze
            end
          end
        end
      end

      # Parser is a recursive-descent parser of RE2 syntax into a Node tree.
      # Groups are never captured: a condition only asks whether the pattern
      # matches.
      class Parser
        def initialize(pattern)
          pattern = pattern.dup.force_encoding(Encoding::UTF_8)
          raise Error, "invalid UTF-8" unless pattern.valid_encoding?

          @chars = pattern.chars
          @pos = 0
        end

        def parse
          node = body(0, 0, top: true)
          raise Error, "unexpected )" unless eof?
          raise Error, "expression too large" if node.size > MAX_SIZE

          node
        end

        private

        def eof? = @pos >= @chars.size

        def peek(offset = 0) = @chars[@pos + offset]

        def advance
          c = @chars[@pos]
          @pos += 1
          c
        end

        def expect(char, message)
          raise Error, message unless peek == char

          advance
        end

        def cat(items) = items.size == 1 ? items[0] : Node.new(Node::CAT, subs: items)

        def alt(subs) = subs.size == 1 ? subs[0] : Node.new(Node::ALT, subs: subs)

        def char(ranges) = Node.new(Node::CHAR, set: CharSet.new(ranges, [], false))

        # body parses alternatives up to an unmatched ) or the end. A flag
        # group without a body changes flags up to the end of the enclosing
        # group, across alternatives, as in RE2.
        def body(flags, depth, top: false)
          alternatives = []
          items = []
          repeated = false
          until eof? || (peek == ")" && !top)
            repeat = false
            case peek
            when ")" then raise Error, "unexpected )"
            when "|"
              advance
              alternatives << cat(items)
              items = []
            when "*", "+", "?", "{" then repeat = repeat_or_literal(items, flags, repeated)
            when "(" then flags = group(items, flags, depth)
            when "\\" then escape(items, flags)
            else items << plain_atom(flags)
            end
            repeated = repeat
          end
          alt([*alternatives, cat(items)])
        end

        # repeat_or_literal applies a repetition to the last item, or reads a
        # { that starts no well-formed repetition as a literal. It reports
        # whether it repeated.
        def repeat_or_literal(items, flags, repeated)
          counts = repetition
          if counts
            repeat(items, *counts, repeated)
            return true
          end

          advance
          items << literal("{".ord, flags)
          false
        end

        def repeat(items, lo, hi, counted, repeated)
          raise Error, "invalid nested repetition operator" if repeated
          raise Error, "missing argument to repetition operator" if items.empty?

          node = Node.new(Node::REPEAT, subs: [items.pop], lo: lo, hi: hi, counted: counted)
          raise Error, "invalid repeat count" if counted && (lo >= 2 || hi >= 2) && node.need > MAX_REPEAT
          raise Error, "expression too large" if node.size > MAX_SIZE

          items << node
        end

        # group reads a group or a flag group and returns the flags in force
        # after it.
        def group(items, flags, depth)
          advance
          raise Error, "expression nests too deeply" if depth + 1 > MAX_DEPTH

          inner = group_flags(flags)
          return inner.first if inner.is_a?(Array)

          items << body(inner, depth + 1)
          expect(")", "missing )")
          flags
        end

        def plain_atom(flags)
          c = advance
          case c
          when "[" then char_class(flags)
          when "." then char(flags.anybits?(DOT_NL) ? ANYTHING : NO_NEWLINE)
          when "^" then Node.new(Node::ASSERT, what: flags.anybits?(MULTILINE) ? Vm::BEGIN_LINE : Vm::BEGIN_TEXT)
          when "$" then Node.new(Node::ASSERT, what: flags.anybits?(MULTILINE) ? Vm::END_LINE : Vm::END_TEXT)
          else literal(c.ord, flags)
          end
        end

        # repetition consumes *, +, ? or a well-formed {n}, {n,} or {n,m},
        # and a lazy ?, returning [lo, hi, counted] (hi -1 when unbounded). A
        # { that does not start a well-formed repetition is a literal: it
        # returns nil and consumes nothing. Laziness never changes whether a
        # string matches.
        def repetition
          c = peek
          counts =
            if c == "{"
              counts = repeat_counts
              return nil unless counts

              lo, hi = counts
              invalid = lo.negative? || lo > MAX_REPEAT || hi > MAX_REPEAT || (hi >= 0 && lo > hi)
              raise Error, "invalid repeat count" if invalid

              [lo, hi, true]
            else
              advance
              [c == "+" ? 1 : 0, c == "?" ? 1 : -1, false]
            end
          advance if peek == "?"
          counts
        end

        # repeat_counts reads {n}, {n,} or {n,m} as [min, max] (max -1 when
        # unbounded) and consumes it, or returns nil. A count of more than
        # eight digits is -1 so that it is invalid, as in RE2.
        def repeat_counts
          saved = @pos
          advance
          min = count
          max = min
          if min && peek == ","
            advance
            max = peek == "}" ? -1 : count
          end
          return [min, max] if min && max && peek == "}" && advance

          @pos = saved
          nil
        end

        def count
          digits = +""
          digits << advance while peek&.match?(/\A[0-9]\z/)
          return nil if digits.empty? || (digits.size > 1 && digits.start_with?("0"))

          digits.size > 8 ? -1 : digits.to_i
        end

        # group_flags reads what follows (. It returns [flags] for a flag
        # group without a body, or the flags of a group's body.
        def group_flags(flags)
          return flags unless peek == "?"

          advance
          if peek == "P" || (peek == "<" && ![nil, "=", "!"].include?(peek(1)))
            advance if peek == "P"
            expect("<", "invalid or unsupported Perl syntax")
            group_name
            return flags
          end
          flag_list(flags)
        end

        def group_name
          name = +""
          name << advance until eof? || peek == ">"
          raise Error, "invalid named capture" if eof? || name.empty? || !name.match?(/\A[0-9A-Za-z_]+\z/)

          advance
        end

        def flag_list(flags)
          negated = false
          seen = false
          until eof?
            c = advance
            case c
            when "i", "m", "s", "U"
              bit = { "i" => FOLD, "m" => MULTILINE, "s" => DOT_NL, "U" => 0 }.fetch(c)
              flags = negated ? flags & ~bit : flags | bit
              seen = true
            when "-"
              raise Error, "invalid or unsupported Perl syntax" if negated

              negated = true
              seen = false
            when ":", ")"
              raise Error, "missing flags" if negated && !seen

              return c == ")" ? [flags] : flags
            else
              raise Error, "invalid or unsupported Perl syntax"
            end
          end
          raise Error, "missing )"
        end

        # escape adds what follows a backslash to items; every character of
        # \Q...\E is an operand of its own.
        def escape(items, flags)
          atom = escape_atom(flags)
          atom.is_a?(Array) ? items.concat(atom) : items << atom
        end

        # escape_atom translates what follows a backslash outside a class.
        # \Q...\E yields the list of its literals.
        def escape_atom(flags)
          advance
          raise Error, "trailing backslash" if eof?

          case peek
          when "A" then advance && Node.new(Node::ASSERT, what: Vm::BEGIN_TEXT)
          when "z" then advance && Node.new(Node::ASSERT, what: Vm::END_TEXT)
          when "b" then advance && Node.new(Node::ASSERT, what: Vm::WORD_BOUNDARY)
          when "B" then advance && Node.new(Node::ASSERT, what: Vm::NOT_WORD_BOUNDARY)
          when "Q" then advance && quoted_literals(flags)
          when "d", "s", "w", "D", "S", "W"
            ranges, props = perl_class(flags)
            set(ranges, props, negated: false)
          when "p", "P"
            ranges, props = unicode_class(flags)
            set(ranges, props, negated: false)
          else literal(escaped_rune, flags)
          end
        end

        def quoted_literals(flags)
          out = []
          until eof?
            if peek == "\\" && peek(1) == "E"
              @pos += 2
              break
            end
            out << literal(advance.ord, flags)
          end
          out
        end

        # escaped_rune reads an escape denoting one code point, the
        # backslash already consumed.
        def escaped_rune
          c = advance
          case c
          when "1".."7"
            raise Error, "invalid escape sequence" unless peek&.match?(/\A[0-7]\z/)

            octal(c)
          when "0" then octal(c)
          when "x" then hex
          else
            return SIMPLE_ESCAPES.fetch(c) if SIMPLE_ESCAPES.key?(c)
            return c.ord if c.ord < 0x80 && !c.match?(/\A[0-9A-Za-z]\z/)

            raise Error, "invalid escape sequence"
          end
        end

        def octal(first)
          digits = first.dup
          digits << advance while digits.size < 3 && peek&.match?(/\A[0-7]\z/)
          digits.to_i(8)
        end

        def hex
          raise Error, "invalid escape sequence" if eof?

          if peek == "{"
            advance
            digits = +""
            digits << advance while peek&.match?(/\A\h\z/)
            raise Error, "invalid escape sequence" if digits.empty? || peek != "}" || digits.to_i(16) > MAX_RUNE

            advance
            return digits.to_i(16)
          end
          digits = [advance, advance].join
          raise Error, "invalid escape sequence" unless digits.match?(/\A\h\h\z/)

          digits.to_i(16)
        end

        # perl_class reads \d, \s, \w or a negation, the backslash consumed,
        # as [ranges, props].
        def perl_class(flags)
          c = advance
          ranges = PERL_CLASSES.fetch(c.downcase)
          ranges = Sets.fold(ranges) if flags.anybits?(FOLD)
          [c == c.upcase ? Sets.complement(ranges) : ranges, []]
        end

        # unicode_class reads \pN, \p{Name}, \p{^Name} or a \P negation,
        # the backslash consumed, as [ranges, props].
        def unicode_class(flags)
          negated = advance == "P"
          raise Error, "invalid character class range" if eof?

          name = advance
          if name == "{"
            name = +""
            name << advance until eof? || peek == "}"
            expect("}", "invalid character class range")
          end
          if name.start_with?("^")
            negated = !negated
            name = name[1..]
          end
          raise Error, "invalid character class range" unless PROPERTY_NAMES.key?(name)

          [[], [Sets.property(name, negated, flags.anybits?(FOLD))]]
        end

        # char_class reads a class after its [.
        def char_class(flags)
          negated = peek == "^"
          advance if negated
          ranges = []
          props = []
          first = true
          while eof? || peek != "]" || first
            first = false
            r, p = class_item(flags)
            ranges.concat(r)
            props.concat(p)
          end
          advance
          set(ranges, props, negated: negated)
        end

        def class_item(flags)
          if peek == "[" && peek(1) == ":"
            posix = posix_class(flags)
            return posix if posix
          end
          if peek == "\\" && %w[p P].include?(peek(1))
            advance
            return unicode_class(flags)
          end
          if peek == "\\" && %w[d s w D S W].include?(peek(1))
            advance
            return perl_class(flags)
          end
          lo = class_char
          hi = lo
          if peek == "-" && ![nil, "]"].include?(peek(1))
            advance
            hi = class_char
            raise Error, "invalid character class range" if hi < lo
          end
          ranges = [[lo, hi]]
          [flags.anybits?(FOLD) ? Sets.fold(ranges) : ranges, []]
        end

        # posix_class reads [:name:] or [:^name:], or returns nil when no :]
        # follows, making the [ a literal.
        def posix_class(flags)
          close = @chars[(@pos + 2)..].each_cons(2).find_index { |a, b| a == ":" && b == "]" }
          return nil unless close

          name = @chars[@pos + 2, close].join
          negated = name.start_with?("^")
          ranges = POSIX_CLASSES[negated ? name[1..] : name]
          raise Error, "invalid character class range" unless ranges

          @pos += close + 4
          ranges = Sets.fold(ranges) if flags.anybits?(FOLD)
          [negated ? Sets.complement(ranges) : ranges, []]
        end

        def class_char
          raise Error, "missing closing ]" if eof?

          c = advance
          return c.ord unless c == "\\"
          raise Error, "trailing backslash" if eof?

          escaped_rune
        end

        def literal(code_point, flags)
          ranges = [[code_point, code_point]]
          char(flags.anybits?(FOLD) ? Sets.fold(ranges) : ranges)
        end

        # set builds the class of a [...] or an escape.
        def set(ranges, props, negated:)
          Node.new(Node::CHAR, set: CharSet.new(Sets.normalize(ranges), props, negated))
        end
      end
      private_constant :Parser, :Sets
    end
    private_constant :Regex, :UnicodeTables
  end
end
