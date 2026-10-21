# store.rb: the Ruby sample of the characterization tests.
module Sample
  class Store
    attr_reader :name

    def initialize(name)
      @name = name
    end

    # reload in a comment is no reference.
    def reload
      label = 'reload in a string is no reference'
      puts label
    end

    def self.build(name)
      new(name)
    end

    class Nested
      def reload_nested
        1
      end
    end
  end

  def self.helper
    'reload'
  end
end

=begin
def reload is no declaration inside a block comment
=end
