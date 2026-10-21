<?php

namespace Sample;

/** Store reloads the items; a reload keeps the order. */
class Store
{
    private const MAX_ITEMS = 10;

    /** reload the items */
    public function reload(): void
    {
        $note = 'reload kept the order';
        echo $note;
    }

    public function find(string $name): string
    {
        return $name;
    }
}

trait Reloadable
{
    public function reload(): void
    {
    }
}

interface Finder
{
    public function find(string $name): string;
}
