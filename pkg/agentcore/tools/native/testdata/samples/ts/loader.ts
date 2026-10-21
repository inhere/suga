// Loader reloads the configuration; a reload keeps the keys.
export interface LoaderOptions {
  reload: boolean;
  retries?: number;
}

export class Loader {
  private count = 0;

  constructor(private readonly opts: LoaderOptions) {}

  /** reload the configuration */
  reload(): void {
    const note = "reload kept the keys";
    this.count++;
    console.log(note);
  }

  static create(opts: LoaderOptions): Loader {
    return new Loader(opts);
  }
}

export enum Mode {
  Fast,
  Slow,
}

export function reload(): void {
  const loader = new Loader({ reload: true });
  loader.reload();
}
