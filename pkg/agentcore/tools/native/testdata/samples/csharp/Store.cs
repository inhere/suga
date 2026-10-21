// Store.cs: the C# sample of the characterization tests (Allman braces).
using System;
using System.Collections.Generic;

namespace Sample.Store
{
    public class Store
    {
        private readonly List<string> items = new();

        public Store(int capacity)
        {
            // "Reload" in a comment is no reference.
            items.Capacity = capacity; Reload();
        }

        public void Reload()
        {
            string label = "Reload in a string is no reference";
            Console.WriteLine(label);
        }

        public string Describe(int width)
        {
            return $"Reload {width}";
        }

        private bool ready { get; set; }
    }

    public interface ILoader
    {
        void Load(string path);
    }

    public enum Mode
    {
        Fast,
        Slow,
    }
}
