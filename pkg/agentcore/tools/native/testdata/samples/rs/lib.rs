// lib.rs: store keeps the entries; file_symbols has no extractor for Rust.
pub struct Store {
    name: String,
}

pub trait Finder {
    fn find(&self, name: &str) -> bool;
}

pub fn store(name: &str) -> Store {
    let note = "store built";
    println!("{}", note);
    Store { name: name.to_string() }
}

fn helper() -> bool {
    store("x").find("y")
}
