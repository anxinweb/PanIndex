package Util

import (
	"bytes"
	"encoding/json"
	"fmt"
	jsoniter "github.com/json-iterator/go"
	"github.com/libsgh/PanIndex/config"
	"github.com/libsgh/PanIndex/entity"
	"github.com/libsgh/PanIndex/model"
	"github.com/libsgh/nic"
	uuid "github.com/satori/go.uuid"
	log "github.com/sirupsen/logrus"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"time"
)

var Alis = map[string]entity.TokenResp{}

func AliRefreshToken(account entity.Account) string {
	defer func() {
		if p := recover(); p != nil {
			log.Errorln(p)
		}
	}()
	resp, err := nic.Post("https://auth.aliyundrive.com/v2/account/token", nic.H{
		JSON: nic.KV{
			"refresh_token": account.RefreshToken,
			"grant_type":    "refresh_token",
		},
	})
	if err != nil {
		panic(err.Error())
		return ""
	}
	var tokenResp entity.TokenResp
	err = jsoniter.Unmarshal(resp.Bytes, &tokenResp)
	if err != nil {
		panic(err.Error())
		return ""
	}
	Alis[account.Id] = tokenResp
	return tokenResp.RefreshToken
}

func AliGetFiles(accountId, rootId, fileId, p string, hide, hasPwd int, syncChild bool) {
	tokenResp := Alis[accountId]
	auth := tokenResp.TokenType + " " + tokenResp.AccessToken
	defer func() {
		if p := recover(); p != nil {
			log.Errorln(p)
		}
	}()
	limit := 100
	nextMarker := ""
	for {
		resp, err := nic.Post("https://api.aliyundrive.com/v2/file/list", nic.H{
			Headers: nic.KV{
				"authorization": auth,
			},
			JSON: nic.KV{
				"all":                     false,
				"drive_id":                tokenResp.UserInfo.DefaultDriveId,
				"fields":                  "*",
				"image_thumbnail_process": "image/resize,w_400/format,jpeg",
				"image_url_process":       "image/resize,w_1920/format,jpeg",
				"limit":                   limit,
				"marker":                  nextMarker,
				"order_by":                "updated_at",
				"order_direction":         "DESC",
				"parent_file_id":          fileId,
				"video_thumbnail_process": "video/snapshot,t_0,f_jpg,ar_auto,w_300",
			},
		})
		if err != nil {
			panic(err.Error())
		}
		byteFiles := []byte(resp.Text)
		d := jsoniter.Get(byteFiles, "items")
		nextMarker = jsoniter.Get(byteFiles, "next_marker").ToString()
		var m []map[string]interface{}
		json.Unmarshal([]byte(d.ToString()), &m)
		for _, item := range m {
			fn := entity.FileNode{}
			fn.AccountId = accountId
			fn.FileId = item["file_id"].(string)
			fn.FileName = item["name"].(string)
			fn.FileIdDigest = ""
			fn.CreateTime = UTCTimeFormat(item["created_at"].(string))
			fn.LastOpTime = UTCTimeFormat(item["updated_at"].(string))
			fn.Delete = 1
			kind := item["type"].(string)
			if kind == "file" {
				if item["file_extension"] == nil {
					fn.FileType = ""
				} else {
					fn.FileType = item["file_extension"].(string)
				}
				fn.IsFolder = false
				fn.FileSize = int64(item["size"].(float64))
				fn.SizeFmt = FormatFileSize(fn.FileSize)
				category := item["category"].(string)
				if category == "image" {
					//??????
					fn.MediaType = 1
				} else if category == "doc" {
					//??????
					fn.MediaType = 4
				} else if category == "video" {
					//??????
					fn.MediaType = 3
				} else if category == "audio" {
					//??????
					fn.MediaType = 2
				} else {
					//????????????
					fn.MediaType = 0
				}
				fn.DownloadUrl = item["download_url"].(string)
			} else {
				fn.FileType = ""
				fn.IsFolder = true
				fn.FileSize = 0
				fn.SizeFmt = "-"
				fn.MediaType = 0
				fn.DownloadUrl = ""
			}
			//????????????????????????????????????????????????
			fn.IsStarred = item["starred"].(bool)
			fn.ParentId = item["parent_file_id"].(string)
			fn.Hide = 0
			fn.HasPwd = 0
			if hide == 1 {
				fn.Hide = hide
			} else {
				if config.GloablConfig.HideFileId != "" {
					listSTring := strings.Split(config.GloablConfig.HideFileId, ",")
					sort.Strings(listSTring)
					i := sort.SearchStrings(listSTring, fn.FileId)
					if i < len(listSTring) && listSTring[i] == fn.FileId {
						fn.Hide = 1
					}
				}
			}
			if hasPwd == 1 {
				fn.HasPwd = hasPwd
			} else {
				if config.GloablConfig.PwdDirId != "" {
					listSTring := strings.Split(config.GloablConfig.PwdDirId, ",")
					sort.Strings(listSTring)
					i := sort.SearchStrings(listSTring, fn.FileId)
					if i < len(listSTring) && strings.Split(listSTring[i], ":")[0] == fn.FileId {
						fn.HasPwd = 1
					}
				}
			}
			fn.ParentPath = p
			if p == "/" {
				fn.Path = p + fn.FileName
			} else {
				fn.Path = p + "/" + fn.FileName
			}
			if fn.IsFolder == true {
				if syncChild {
					AliGetFiles(accountId, rootId, fn.FileId, fn.Path, fn.Hide, fn.HasPwd, syncChild)
				}
			}
			fn.Id = uuid.NewV4().String()
			fn.CacheTime = time.Now().UnixNano()
			model.SqliteDb.Create(fn)
		}
		if nextMarker == "" {
			break
		}
	}

}
func AliGetDownloadUrl(accountId, fileId string) string {
	tokenResp := Alis[accountId]
	auth := tokenResp.TokenType + " " + tokenResp.AccessToken
	resp, err := nic.Post("https://api.aliyundrive.com/v2/file/get_download_url", nic.H{
		Headers: nic.KV{
			"authorization": auth,
		},
		JSON: nic.KV{
			"drive_id":   tokenResp.UserInfo.DefaultDriveId,
			"file_id":    fileId,
			"expire_sec": 14400,
		},
	})
	if err != nil {
		return ""
	}
	fmt.Println(resp.Text)
	downUrl := jsoniter.Get(resp.Bytes, "url").ToString()
	speedLimit := jsoniter.Get(resp.Bytes, "ratelimit").Get("part_speed").ToInt()
	if downUrl == "" {
		log.Warningln("????????????????????????????????????")
	}
	if speedLimit != -1 {
		log.Warningf("??????????????????%d", speedLimit)
	}
	return downUrl
}

func AliFolderDownload(accountId, fileId, archiveName, ua string) string {
	tokenResp := Alis[accountId]
	auth := tokenResp.TokenType + " " + tokenResp.AccessToken
	resp, err := nic.Post("https://api.aliyundrive.com/adrive/v1/file/multiDownloadUrl", nic.H{
		Headers: nic.KV{
			"authorization": auth,
			"User-Agent":    ua,
		},
		JSON: nic.KV{
			"download_infos": []nic.KV{nic.KV{
				"drive_id": tokenResp.DefaultDriveId,
				"files": []nic.KV{nic.KV{
					"file_id": fileId,
				}},
			},
			},
			"archive_name": archiveName,
		},
	})
	if err != nil {
		return ""
	}
	downUrl := jsoniter.Get(resp.Bytes, "download_url").ToString()
	if downUrl == "" {
		log.Warningln("????????????????????????????????????")
	}
	return downUrl
}

func AliUpload(accountId, parentId string, files []*multipart.FileHeader) bool {
	tokenResp := Alis[accountId]
	auth := tokenResp.TokenType + " " + tokenResp.AccessToken
	for _, file := range files {
		t1 := time.Now()
		log.Debugf("?????????????????????%s????????????%d", file.Filename, file.Size)
		resp, _ := nic.Post("https://api.aliyundrive.com/v2/file/create_with_proof", nic.H{
			Headers: nic.KV{
				"authorization": auth,
			},
			JSON: nic.KV{
				"drive_id": tokenResp.DefaultDriveId,
				"part_info_list": []nic.KV{nic.KV{
					"part_number": 1,
				},
				},
				"pre_hash":        "",
				"parent_file_id":  parentId,
				"name":            file.Filename,
				"type":            "file",
				"check_name_mode": "auto_rename",
				"size":            file.Size,
			},
		})
		rapidUpload := jsoniter.Get(resp.Bytes, "rapid_upload").ToBool()
		if rapidUpload {
			//????????????
			log.Debugf("?????????????????????%s", resp.Text)
			log.Debugf("?????????%s???????????????????????????%s", file.Filename, ShortDur(time.Now().Sub(t1)))
			return true
		}
		fileId := jsoniter.Get(resp.Bytes, "file_id").ToString()
		uploadId := jsoniter.Get(resp.Bytes, "upload_id").ToString()
		driveId := jsoniter.Get(resp.Bytes, "drive_id").ToString()
		partInfoListString := jsoniter.Get(resp.Bytes, "part_info_list").ToString()
		partInfoList := []entity.AliPartInfo{}
		jsoniter.UnmarshalFromString(partInfoListString, &partInfoList)
		log.Debugf("??????????????????%d", len(partInfoList))
		for _, partInfo := range partInfoList {
			fileContent, _ := file.Open()
			byteContent, _ := ioutil.ReadAll(fileContent)
			client := &http.Client{}
			req, err := http.NewRequest(http.MethodPut, partInfo.UploadUrl, bytes.NewBuffer(byteContent))
			if err != nil {
				log.Error("????????????")
				return false
			}
			client.Do(req)
		}
		resp, _ = nic.Post("https://api.aliyundrive.com/v2/file/complete", nic.H{
			Headers: nic.KV{
				"authorization": auth,
			},
			JSON: nic.KV{
				"drive_id":  driveId,
				"file_id":   fileId,
				"upload_id": uploadId,
			},
		})
		log.Debugf("?????????????????????%s", resp.Text)
		log.Debugf("?????????%s???????????????????????????%s", file.Filename, ShortDur(time.Now().Sub(t1)))
	}
	return true
}

//???????????????
func AliTranscoding(accountId, fileId string) string {
	tokenResp := Alis[accountId]
	auth := tokenResp.TokenType + " " + tokenResp.AccessToken
	resp, _ := nic.Post("https://api.aliyundrive.com/v2/file/get_video_preview_play_info", nic.H{
		Headers: nic.KV{
			"authorization": auth,
		},
		JSON: nic.KV{
			"category":    "live_transcoding",
			"drive_id":    tokenResp.DefaultDriveId,
			"file_id":     fileId,
			"template_id": "",
		},
	})
	return resp.Text
}
