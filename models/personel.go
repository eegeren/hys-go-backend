package models

type GirisRequest struct {
	TCKimlikNo string `json:"tc_kimlik_no"`
}

type PersonelDetayRequest struct {
	InsanID int `json:"insan_id"`
}

type Personel struct {
	InsanID int    `json:"insan_id"`
	TC      string `json:"tc"`
	Ad      string `json:"ad"`
	Soyad   string `json:"soyad"`
	Sube    string `json:"sube"`
	Gorev   string `json:"gorev"`
}
