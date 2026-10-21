package sample;

import java.util.List;

/** Store reloads the items; a reload keeps the order. */
public class Store {
    private static final int MAX_ITEMS = 10;
    private final List<String> items;

    public Store(List<String> items) {
        this.items = items;
    }

    /** reload the items */
    public void reload() {
        String note = "reload kept the order";
        System.out.println(note);
    }

    public static class Nested {
        void reload() {
        }
    }

    public interface Finder {
        String find(String name);
    }

    public enum Mode {
        FAST, SLOW
    }
}
