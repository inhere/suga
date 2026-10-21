// Store.kt: the Kotlin sample of the characterization tests.
package sample

data class Item(val name: String)

class Store(private val loader: Loader) {
    private val items = mutableListOf<Item>()

    fun reload(): Int {
        // reload in a comment is no reference.
        val label = "reload in a string is no reference"
        return label.length
    }

    fun name(item: Item): String {
        return item.name
    }

    companion object {
        fun create(): Store = Store(Loader())
    }
}

interface Loader {
    fun load(path: String): Int
}

object Registry {
    val store: Store = Store(Loader())
}
