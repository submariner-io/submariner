package kubernetes

import "testing"

func TestNegStringSliceOverlaps(t *testing.T) {

    var left1 = []string { "haystack", "small" };
    var right1 = []string { "needle", "big" };

    if stringSliceOverlaps(left1, right1) {
        t.Errorf("%v and %v should not overlap.", left1, right1);
    }
}

func TestStringSliceOverlaps(t *testing.T) {

    var left1 = []string { "haystack", "needle", "small" };
    var right1 = []string { "needle", "big" };

    if !stringSliceOverlaps(left1, right1) {
        t.Errorf("%v and %v should overlap.", left1, right1);
    }
}
