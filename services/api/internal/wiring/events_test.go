package wiring

import (
	"os"
	"strings"
	"testing"

	invoicingapp "github.com/CodyLionVivo/duradent/services/api/internal/invoicing/application"
	notifapp "github.com/CodyLionVivo/duradent/services/api/internal/notifications/application"
)

// An event a consumer handles but its SNS filter policy drops would never
// arrive: every consumed type must be listed for LocalStack and for AWS.
func TestFilterPoliciesListEveryConsumedEvent(t *testing.T) {
	for _, path := range []string{"../../../../infra/local/localstack/init-aws.sh", "../../../../infra/terraform/modules/stack/main.tf"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, typ := range append(append([]string{}, notifapp.Events...), invoicingapp.Events...) {
			if !strings.Contains(string(raw), `"`+typ+`"`) {
				t.Errorf("%s does not route %s", path, typ)
			}
		}
	}
}
