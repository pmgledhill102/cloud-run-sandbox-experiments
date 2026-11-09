import unittest

from calc import mean


class MeanTest(unittest.TestCase):
    def test_mean(self):
        self.assertEqual(mean([1, 2, 3, 4]), 2.5)

    def test_single(self):
        self.assertEqual(mean([7]), 7)


if __name__ == "__main__":
    unittest.main()
