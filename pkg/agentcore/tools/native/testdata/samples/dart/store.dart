// store.dart: the Dart/Flutter sample of the characterization tests.
import 'package:flutter/material.dart';

/// A loader that reloads the store: `reload` in this doc comment is no use.
abstract class Loader {
  Future<int> load(String path);
}

enum LoaderState { idle, loading, failed }

mixin Retryable {
  int retries = 0;
}

extension LoaderExt on Loader {
  Future<int> reload() => load('reload');
}

extension type LoaderId(int value) {}

typedef LoaderFactory = Loader Function();

class Store {
  Store(this.loader);

  Store.memory() : loader = MemoryLoader();

  factory Store.create(Loader loader) => Store(loader);

  static const int maxItems = 10;
  final Loader loader;
  late final List<String> items = <String>[];

  double get usage => items.length / maxItems;

  set capacity(int value) {
    // reload in a comment is no reference.
    print("reload in a string is no reference");
  }

  Future<int> reload() async {
    return loader.load('store');
  }
}

class StoreWidget extends StatefulWidget {
  const StoreWidget({super.key, required this.store});

  final Store store;

  @override
  State<StoreWidget> createState() => _StoreWidgetState();
}

class _StoreWidgetState extends State<StoreWidget> {
  @override
  Widget build(BuildContext context) {
    return Text('items: ${widget.store.reload()}');
  }
}

/// A stateless badge: its build method is a rebuild-free `const` one.
class StoreBadge extends StatelessWidget {
  const StoreBadge({super.key, required this.count});

  static const String label = 'store badge';

  final int count;

  @override
  Widget build(BuildContext context) => Text('$label: $count');
}

Future<Store> openStore() async {
  final store = Store(MemoryLoader());
  await store.reload();
  return store;
}
