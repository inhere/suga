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
            // "reload" in a comment is no reference.
            items.Capacity = capacity;
        }

        public void Reload()
        {
            string label = "reload in a string is no reference";
            Console.WriteLine(label);
        }

        public string Describe(int width)
        {
            return $"reload {width}";
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
