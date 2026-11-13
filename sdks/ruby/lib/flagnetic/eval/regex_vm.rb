# frozen_string_literal: true

module Flagnetic
  # Eval is the evaluator; see flagnetic/eval.rb.
  module Eval
    module Regex
      # CharSet is a set of code points: inclusive [lo, hi] ranges and Unicode
      # properties, or everything else when negated. It answers membership in
      # constant time for ASCII and in logarithmic time otherwise.
      class CharSet
        attr_reader :ranges

        def initialize(ranges, props, negated)
          @ranges = ranges.freeze
          @los = ranges.map(&:first).freeze
          @his = ranges.map(&:last).freeze
          @props = props.freeze
          @negated = negated
          @ascii = Array.new(128) { |cp| member?(cp) }.freeze
          freeze
        end

        # single reports the one code point the set holds, or nil.
        def single
          @ranges[0][0] if !@negated && @props.empty? && @ranges.size == 1 && @ranges[0][0] == @ranges[0][1]
        end

        def include?(cp)
          cp < 128 ? @ascii[cp] : member?(cp)
        end

        private

        def member?(cp)
          found = in_ranges?(cp) || @props.any? { |prop| prop.include?(cp) }
          found != @negated
        end

        def in_ranges?(cp)
          i = @his.bsearch_index { |hi| hi >= cp }
          !i.nil? && @los[i] <= cp
        end
      end

      # Property is a \p or \P class: the code points with the Unicode
      # property name, or without it when negated, and, under (?i), those
      # an orbit of simple case folding ties to one that has it (extra).
      class Property
        def initialize(name, negated, extra)
          @pattern = Regexp.new("\\p{#{name}}")
          @negated = negated
          @extra = extra.to_h { |cp| [cp, true] }.freeze
          freeze
        end

        def include?(cp)
          has = @pattern.match?([cp].pack("U")) || @extra.key?(cp)
          has != @negated
        end
      end

      # Node is a node of the parse tree. need is the smallest repeat budget
      # Go's repeatIsValid accepts for the subtree, size its approximate
      # length in instructions.
      class Node
        CHAR = 0
        ASSERT = 1
        CAT = 2
        ALT = 3
        REPEAT = 4

        attr_reader :kind, :set, :what, :subs, :lo, :hi, :need, :size

        def initialize(kind, set: nil, what: 0, subs: [], lo: 0, hi: 0, counted: false)
          @kind = kind
          @set = set
          @what = what
          @subs = subs
          @lo = lo
          @hi = hi
          @need = 0
          @size = 1
          measure(counted)
          freeze
        end

        private

        def measure(counted)
          case @kind
          when CAT
            @need = @subs.map(&:need).max || 0
            @size = @subs.sum(&:size)
          when ALT
            @need = @subs.map(&:need).max || 0
            @size = @subs.sum(&:size) + @subs.size - 1
          when REPEAT
            measure_repeat(@subs[0], counted)
          end
        end

        def measure_repeat(inner, counted)
          if counted
            bound = @hi >= 0 ? @hi : @lo
            @need = bound.zero? ? 0 : bound * [inner.need, 1].max
            @size = (inner.size * [@hi >= 0 ? @hi : @lo + 1, 1].max) + 3
          else
            @need = inner.need
            @size = inner.size + 2
          end
        end
      end

      # Program is a compiled pattern: parallel arrays of an opcode, two
      # operands and a set per instruction.
      Program = Struct.new(:ops, :x, :y, :sets)

      # Compiler turns a parse tree into the instructions of a Pike VM.
      # LIT matches one code point (operand x), SET one of a set, SPLIT forks
      # to x and y, JMP goes to x, ASSERT checks the assertion x and goes on
      # to the next instruction, MATCH ends.
      class Compiler
        LIT = 0
        SET = 1
        SPLIT = 2
        JMP = 3
        ASSERT = 4
        MATCH = 5

        def self.compile(root)
          new.compile(root)
        end

        def initialize
          @program = Program.new([], [], [], [])
        end

        # compile walks the tree with a work list, not recursion: a tree can
        # be a few thousand levels deep, more than the stack of a thread or a
        # fiber holds. An entry is a Node to emit or a Proc to run, such as
        # one patching a jump once its target is known.
        def compile(root)
          @work = [root]
          until @work.empty?
            item = @work.pop
            item.is_a?(Node) ? emit_node(item) : item.call
          end
          emit(MATCH)
          @program.each(&:freeze)
          @program.freeze
        end

        private

        def emit(op, x = 0, y = 0, set = nil)
          @program.ops << op
          @program.x << x
          @program.y << y
          @program.sets << set
          @program.ops.size - 1
        end

        def here = @program.ops.size

        # schedule queues steps to run in the order given, before anything
        # queued earlier.
        def schedule(steps)
          @work.concat(steps.reverse)
        end

        def emit_node(node)
          case node.kind
          when Node::CHAR then emit_char(node.set)
          when Node::ASSERT then emit(ASSERT, node.what)
          when Node::CAT then schedule(node.subs)
          when Node::ALT then emit_alternation(node.subs)
          else emit_repeat(node)
          end
        end

        def emit_char(set)
          cp = set.single
          cp ? emit(LIT, cp) : emit(SET, 0, 0, set)
        end

        # split(a, L1); a; jmp end; L1: split(b, L2); b; jmp end; L2: c
        def emit_alternation(subs)
          exits = []
          steps = []
          subs.each_with_index do |sub, k|
            next steps << sub if k == subs.size - 1

            fork = nil
            steps << -> { fork = emit(SPLIT, here + 1) }
            steps << sub
            steps << lambda {
              exits << emit(JMP)
              @program.y[fork] = here
            }
          end
          steps << -> { exits.each { |e| @program.x[e] = here } }
          schedule(steps)
        end

        def emit_repeat(node)
          sub = node.subs[0]
          steps = Array.new(node.lo, sub)
          if node.hi.negative?
            loop_steps(sub, steps)
          else
            # Each optional copy may skip to the end: the language of x{lo,hi}.
            forks = []
            (node.hi - node.lo).times do
              steps << -> { forks << emit(SPLIT, here + 1) }
              steps << sub
            end
            steps << -> { forks.each { |f| @program.y[f] = here } }
          end
          schedule(steps)
        end

        # L: split(body, out); body; jmp L
        def loop_steps(sub, steps)
          fork = nil
          steps << -> { fork = emit(SPLIT, here + 1) }
          steps << sub
          steps << lambda {
            emit(JMP, fork)
            @program.y[fork] = here
          }
        end
      end

      # Vm is a Pike VM: it matches in time linear in the input for a given
      # program, whatever the pattern, so nothing a context carries can make a
      # match take longer.
      module Vm
        LIT = Compiler::LIT
        SET = Compiler::SET
        SPLIT = Compiler::SPLIT
        JMP = Compiler::JMP
        ASSERT = Compiler::ASSERT
        MATCH = Compiler::MATCH

        BEGIN_TEXT = 0
        END_TEXT = 1
        BEGIN_LINE = 2
        END_LINE = 3
        WORD_BOUNDARY = 4
        NOT_WORD_BOUNDARY = 5

        NEWLINE = 0x0A
        REPLACEMENT_CHARACTER = 0xFFFD

        module_function

        # search reports whether program matches anywhere in cps, the code
        # points of the text. Every thread advances in lockstep over them;
        # the first to reach MATCH decides, as only whether it matches is
        # asked.
        def search(program, cps)
          state = State.new(program, cps)
          pos = 0
          loop do
            state.add(state.current, 0, pos) # unanchored: a new thread at every position
            return true if state.step(pos)
            return false if pos >= cps.size

            pos += 1
            state.advance
          end
        end

        # State holds the threads of one search.
        class State
          attr_reader :current

          def initialize(program, cps)
            @ops = program.ops
            @x = program.x
            @y = program.y
            @sets = program.sets
            @cps = cps
            @mark = Array.new(@ops.size, -1)
            @current = []
            @following = []
            @stack = []
            @gen = 0
          end

          def advance
            @gen += 1
            @current, @following = @following, @current
            @following.clear
          end

          # step runs every thread on the code point at pos and queues the
          # survivors; true when one has matched.
          def step(pos)
            cp = pos < @cps.size ? @cps[pos] : -1
            @current.each do |pc|
              op = @ops[pc]
              return true if op == MATCH
              next if cp.negative?

              add(@following, pc + 1, pos + 1, @gen + 1) if consumes?(op, pc, cp)
            end
            false
          end

          # add queues the threads reachable from pc without consuming input.
          def add(threads, pc, pos, gen = @gen)
            @stack.push(pc)
            until @stack.empty?
              pc = @stack.pop
              next if @mark[pc] == gen

              @mark[pc] = gen
              case @ops[pc]
              when JMP then @stack.push(@x[pc])
              when SPLIT then @stack.push(@y[pc], @x[pc])
              when ASSERT then @stack.push(pc + 1) if holds?(@x[pc], pos)
              else threads << pc
              end
            end
          end

          private

          def consumes?(op, pc, cp)
            op == LIT ? cp == @x[pc] : op == SET && @sets[pc].include?(cp)
          end

          # \b and \B read ASCII word characters, as RE2 does.
          def holds?(assertion, pos)
            n = @cps.size
            case assertion
            when BEGIN_TEXT then pos.zero?
            when END_TEXT then pos == n
            when BEGIN_LINE then pos.zero? || @cps[pos - 1] == NEWLINE
            when END_LINE then pos == n || @cps[pos] == NEWLINE
            else (word?(pos - 1) != word?(pos)) == (assertion == WORD_BOUNDARY)
            end
          end

          def word?(pos)
            return false if pos.negative? || pos >= @cps.size

            cp = @cps[pos]
            cp == 0x5F || cp.between?(0x30, 0x39) || cp.between?(0x41, 0x5A) || cp.between?(0x61, 0x7A)
          end
        end
      end
    end
  end
end
