## MetroMan Emulator Testing
1. Use Raccoon APK downloader
2. Note that `/Users/[username]/Raccoon/content/apps/com.xinlukou.metroman` should exist
3. Run `java -jar APKEditor-1.4.5.jar m -i "/Users/[username]/Raccoon/content/apps/com.xinlukou.metroman"` and move the merged APK to downloads
4. Run the standard `apk-mitm com.xinlukou.metroman_merged.apk --certificate charles-ssl-proxying-certificate.pem`
5. Run that APK in the emulator with the proxy set

# Story
1. Started with Baidu Maps, found the web version lacking in providing timetables. It generally assumed the subways just took a certain amount of time from the current time, like walking
2. Looked for other apps with chinese timetables. Found MetroMan which is offline
3. Cert pinned it, ran in Android Emulator with a proxy
4. Found that it simply downloaded a zip from their central server that was named with a date. Probably changed relatively often
5. Started working through https://gtfs.org/documentation/schedule/reference
6. Started to use mandarin documentation to figure things out. Like the mandarin article on "station signage" to figure out how station codes are formatted in Beijing (https://zh.wikipedia.org/zh-hans/%E8%BB%8A%E7%AB%99%E7%B7%A8%E8%99%9F) (Beijing for example actually retired their codes and just use the name now)
7. Used prior research into Baidu Maps, and some more snooping, to find the autocomplete request