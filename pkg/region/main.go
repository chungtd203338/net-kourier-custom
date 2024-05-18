package region

func InitRegions() []map[string]string {
	return []map[string]string{
		{
			"id":    "0",
			"name":  "region1",
			"label": "master",
		},
		{
			"id":    "1",
			"name":  "region2",
			"label": "worker1",
		},
		{
			"id":    "2",
			"name":  "region3",
			"label": "worker2",
		},
	}
}
