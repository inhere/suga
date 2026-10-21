// store.cpp: the C/C++ sample of the characterization tests.
#include <string>

namespace sample {

struct Item {
    std::string name;
};

class Store {
public:
    explicit Store(int capacity);
    void reload();
    int size() const;
};

// reload in a comment is no reference.
void Store::reload() {
    const char *label = "reload in a string is no reference";
    (void)label;
}

int load(const char *path) {
    return path ? 1 : 0;
}

} // namespace sample
